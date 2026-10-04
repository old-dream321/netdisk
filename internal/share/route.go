package share

import (
	"crypto/rand"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v5"
	"gorm.io/gorm"

	"netdisk/internal/auth"
	"netdisk/internal/file"
	"netdisk/pkg/httpx"
	"netdisk/pkg/session"
)

// 需要登录的路由
func RegisterRoutes(g *echo.Group, db *gorm.DB, sessions session.Store) {
	g = g.Group("", auth.RequireLogin(sessions))
	g.POST("/create", create(db))
	g.GET("", list(db))
	g.DELETE("/:id", revoke(db))
}

// “打开分享链接”的公开路由
func RegisterPublicRoutes(e *echo.Echo, db *gorm.DB, storageDir string) {
	e.HEAD("/s/:token", get(db, storageDir))
	e.GET("/s/:token", get(db, storageDir))
}

func newToken() string {
	return rand.Text()
}

type createRequest struct {
	FileID    uint64     `json:"file_id"`
	ExpiresAt *time.Time `json:"exp"`
}

func create(db *gorm.DB) echo.HandlerFunc {
	return func(c *echo.Context) error {
		ctx := c.Request().Context()
		ownerID := auth.UserID(c)

		var req createRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "请求体解析失败"})
		}

		if req.FileID == 0 {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "文件ID不能为空"})
		}

		target, err := gorm.G[file.File](db).
			Where("id = ? AND owner_id = ?", req.FileID, ownerID).
			First(ctx)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return c.JSON(http.StatusNotFound, map[string]string{"error": "文件不存在"})
			}
			return httpx.Fail(c, err, "查询文件失败")
		}
		if target.Status != file.StatusNormal {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "文件不在正常状态"})
		}

		if target.Type != file.TypeFile {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "暂不支持分享目录"})
		}

		if req.ExpiresAt != nil && !req.ExpiresAt.After(time.Now()) {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "过期时间必须晚于当前时间"})
		}

		share := Share{
			Token:     newToken(),
			OwnerID:   ownerID,
			FileID:    req.FileID,
			ExpiresAt: req.ExpiresAt,
		}

		if err := gorm.G[Share](db).Create(ctx, &share); err != nil {
			return httpx.Fail(c, err, "分享文件记录失败")
		}

		return c.JSON(http.StatusCreated, map[string]any{
			"token":      share.Token,
			"url":        "/s/" + share.Token,
			"expires_at": share.ExpiresAt,
		})

	}
}

func get(db *gorm.DB, storageDir string) echo.HandlerFunc {
	return func(c *echo.Context) error {
		ctx := c.Request().Context()

		token := c.Param("token")
		if token == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "token不能为空"})
		}

		target, err := gorm.G[Share](db).Where("token = ?", token).First(ctx)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return c.JSON(http.StatusNotFound, map[string]string{"error": "分享不存在"})
			}
			return httpx.Fail(c, err, "查询分享失败")
		}

		if target.ExpiresAt != nil && !target.ExpiresAt.After(time.Now()) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "分享已过期"})
		}

		f, err := gorm.G[file.File](db).Where("id = ?", target.FileID).First(ctx)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return c.JSON(http.StatusNotFound, map[string]string{"error": "分享文件不存在"})
			}
			return httpx.Fail(c, err, "查询分享文件失败")
		}
		if f.Type != file.TypeFile || f.Status != file.StatusNormal || f.StorageKey == "" {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "文件不可用"})
		}

		return file.ServeObject(c, storageDir, f)
	}
}

// 分页上限
const (
	defaultListLimit = 200
	maxListLimit     = 1000
)

func list(db *gorm.DB) echo.HandlerFunc {
	return func(c *echo.Context) error {
		ctx := c.Request().Context()
		ownerID := auth.UserID(c)

		limit := defaultListLimit
		if s := c.QueryParam("limit"); s != "" {
			v, err := strconv.Atoi(s)
			if err != nil || v <= 0 {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": "非法的 limit"})
			}
			limit = min(v, maxListLimit) // 超上限就截断，不报错
		}
		offset := 0
		if s := c.QueryParam("offset"); s != "" {
			v, err := strconv.Atoi(s)
			if err != nil || v < 0 {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": "非法的 offset"})
			}
			offset = v
		}

		shares, err := gorm.G[Share](db).
			Where("owner_id = ?", ownerID).
			Order("created_at DESC, id DESC").
			Limit(limit).
			Offset(offset).
			Find(ctx)
		if err != nil {
			return httpx.Fail(c, err, "查询分享列表失败")
		}

		// 去重，避免重复查询（可能多个分享一个文件）
		fileIDs := make([]uint64, 0, len(shares))
		seen := make(map[uint64]bool, len(shares))
		for _, s := range shares {
			if !seen[s.FileID] {
				seen[s.FileID] = true
				fileIDs = append(fileIDs, s.FileID)
			}
		}

		byID := make(map[uint64]file.File, len(fileIDs))
		if len(fileIDs) > 0 {
			files, err := gorm.G[file.File](db).Where("id IN ?", fileIDs).Find(ctx)
			if err != nil {
				return httpx.Fail(c, err, "查询分享文件失败")
			}
			for _, f := range files {
				byID[f.ID] = f
			}
		}

		now := time.Now()
		items := make([]map[string]any, 0, len(shares))
		for _, s := range shares {
			f, exists := byID[s.FileID]

			name := ""
			if exists {
				name = f.Name
			}
			items = append(items, map[string]any{
				"id":         s.ID,
				"token":      s.Token,
				"url":        "/s/" + s.Token,
				"expires_at": s.ExpiresAt,
				"file_id":    s.FileID,
				"file_name":  name,
				"available":  exists && f.Type == file.TypeFile && f.Status == file.StatusNormal,
				"expired":    s.ExpiresAt != nil && !s.ExpiresAt.After(now),
			})
		}

		return c.JSON(http.StatusOK, map[string]any{
			"items":  items,
			"count":  len(items),
			"limit":  limit,
			"offset": offset,
		})
	}
}

func revoke(db *gorm.DB) echo.HandlerFunc {
	return func(c *echo.Context) error {
		ctx := c.Request().Context()
		ownerID := auth.UserID(c)

		id, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "非法的分享 id"})
		}

		n, err := gorm.G[Share](db).
			Where("id = ? AND owner_id = ?", id, ownerID).
			Delete(ctx)
		if err != nil {
			return httpx.Fail(c, err, "删除分享失败")
		}
		if n == 0 {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "目标分享不存在"})
		}

		return c.NoContent(http.StatusNoContent)
	}
}
