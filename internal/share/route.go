package share

import (
	"crypto/rand"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v5"
	"gorm.io/gorm"

	"netdisk/internal/auth"
	"netdisk/internal/file"
	"netdisk/pkg/httpx"
	"netdisk/pkg/token"
)

// 需要登录的路由
func RegisterRoutes(g *echo.Group, db *gorm.DB, signer *token.Signer) {
	g = g.Group("", auth.RequireLogin(signer))
	g.POST("/create", create(db))
	g.GET("", list(db))
	g.DELETE("/:id", revoke(db))
}

// “打开分享链接”的公开路由。
// 两个路径都要注册：`/s/:token` 匹配根自己，`/s/:token/*` 匹配子路径
func RegisterPublicRoutes(e *echo.Echo, db *gorm.DB, storageDir string) {
	h := serve(db, storageDir)
	e.GET("/s/:token", h)
	e.GET("/s/:token/*", h)
	e.HEAD("/s/:token", h)
	e.HEAD("/s/:token/*", h)
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

func serve(db *gorm.DB, storageDir string) echo.HandlerFunc {
	return func(c *echo.Context) error {
		// 浏览器直接打开分享链接时，跳到前端查看页展示；
		// 程序化访问（curl / Accept: application/json）和下载（download=1）保持原行为。
		// 浏览器导航请求会带 text/html，据此区分。
		if c.Request().Method == http.MethodGet &&
			c.QueryParam("download") != "1" &&
			strings.Contains(c.Request().Header.Get(echo.HeaderAccept), "text/html") {
			target := "/share.html?token=" + url.QueryEscape(c.Param("token"))
			if sub := strings.Trim(c.Param("*"), "/"); sub != "" {
				target += "&path=" + url.QueryEscape(sub)
			}
			return c.Redirect(http.StatusFound, target)
		}

		ctx := c.Request().Context()

		res, err := resolve(ctx, db, c.Param("token"), c.Param("*"))
		if err != nil {
			return writeResolveError(c, err)
		}

		// 下载走同一个路径，只是多一个开关
		if c.QueryParam("download") == "1" {
			if res.Target.Type != file.TypeFile {
				// 目录：流式打包成 zip。打包逻辑和 /api/files/:id/zip 共用，
				// 包括"开始写之后失败只能掐断连接"那套约定。
				wrote, err := file.StreamZipDir(c, db, res.Share.OwnerID, res.Target, storageDir)
				if err != nil {
					if wrote {
						c.Logger().Error("打包分享目录中途失败", "err", err, "dir_id", res.Target.ID)
						panic(http.ErrAbortHandler)
					}
					return httpx.Fail(c, err, "打包分享目录失败")
				}
				return nil
			}
			return file.ServeObject(c, storageDir, res.Target)
		}

		if res.Target.Type == file.TypeDir {
			return viewDir(c, db, res)
		}

		// 单个文件的"展示"
		return c.JSON(http.StatusOK, map[string]any{
			"name":         res.Target.Name,
			"type":         typeName(res.Target.Type),
			"size":         res.Target.Size,
			"expires_at":   res.Share.ExpiresAt,
			"download_url": downloadURL(res.Share.Token, res.Path),
		})
	}
}

// viewDir 列出目录内容。形状和 files/list 保持一致（items/count/limit/offset）。
func viewDir(c *echo.Context, db *gorm.DB, res resolved) error {
	ctx := c.Request().Context()

	limit, offset, err := page(c)
	if err != nil {
		return err
	}

	items, err := gorm.G[file.File](db).
		Where("parent_id = ? AND owner_id = ? AND status = ?",
						res.Target.ID, res.Share.OwnerID, file.StatusNormal).
		Order("type DESC, name ASC"). // 目录(2) 排在文件(1) 前面，和 files/list 一致
		Limit(limit).
		Offset(offset).
		Find(ctx)
	if err != nil {
		return httpx.Fail(c, err, "查询目录内容失败")
	}

	// 子项的路径 = 当前路径 + 自己的名字，用它拼下载地址。
	list := make([]map[string]any, 0, len(items))
	for _, f := range items {
		path := make([]string, 0, len(res.Path)+1)
		path = append(append(path, res.Path...), f.Name)
		list = append(list, map[string]any{
			"name":         f.Name,
			"type":         typeName(f.Type),
			"size":         f.Size,
			"download_url": downloadURL(res.Share.Token, path),
		})
	}

	return c.JSON(http.StatusOK, map[string]any{
		"name":       res.Target.Name,
		"type":       typeName(res.Target.Type),
		"expires_at": res.Share.ExpiresAt,
		"items":      list,
		"count":      len(list),
		"limit":      limit,
		"offset":     offset,
	})
}

func page(c *echo.Context) (limit, offset int, err error) {
	limit = defaultListLimit
	if s := c.QueryParam("limit"); s != "" {
		v, e := strconv.Atoi(s)
		if e != nil || v <= 0 {
			return 0, 0, c.JSON(http.StatusBadRequest, map[string]string{"error": "非法的 limit"})
		}
		limit = min(v, maxListLimit) // 超上限就截断，不报错
	}
	if s := c.QueryParam("offset"); s != "" {
		v, e := strconv.Atoi(s)
		if e != nil || v < 0 {
			return 0, 0, c.JSON(http.StatusBadRequest, map[string]string{"error": "非法的 offset"})
		}
		offset = v
	}
	return limit, offset, nil
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

		limit, offset, err := page(c)
		if err != nil {
			return err
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
				"available":  exists && f.Status == file.StatusNormal,
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
