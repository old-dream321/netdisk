package file

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v5"
	"gorm.io/gorm"

	"netdisk/internal/auth"
	"netdisk/internal/user"
	"netdisk/pkg/httpx"
	"netdisk/pkg/token"
)

const (
	// maxUploadSize 单文件大小上限 1GiB。
	maxUploadSize = 1 << 30
	// 列表默认返回条数与单次上限，避免一次把整个网盘拉出来。
	defaultPageSize = 200
	maxPageSize     = 1000
)

func RegisterRoutes(g *echo.Group, db *gorm.DB, signer *token.Signer, storageDir string) {
	g = g.Group("", auth.RequireLogin(signer))

	g.POST("/list", list(db))
	g.POST("/upload", upload(db, storageDir))
	g.POST("/createdir", createDir(db))
	g.GET("/:id/download", download(db, storageDir))
	// HEAD 和 GET 可以共用同一个 handler，很神奇（
	g.HEAD("/:id/download", download(db, storageDir))
	g.PATCH("/:id", update(db)) // 改名 / 移动
	// 打包下载目录。不注册 HEAD：流式打包给不出 Content-Length，也不支持 Range（边压边发，没法 seek）
	g.GET("/:id/zip", zipDir(db, storageDir))
	g.DELETE("/:id", remove(db, storageDir)) // 默认进回收站，?permanent=true 为彻底删除
	g.POST("/:id/restore", restore(db))      // 从回收站恢复
}

// ---------上传下载-----------

func upload(db *gorm.DB, storageDir string) echo.HandlerFunc {
	return func(c *echo.Context) error {
		ctx := c.Request().Context()
		ownerID := auth.UserID(c)

		header, err := c.FormFile("file")
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "缺少上传文件字段 file"})
		}
		if header.Size > maxUploadSize {
			return c.JSON(http.StatusRequestEntityTooLarge, map[string]string{"error": "文件超过大小限制"})
		}

		if err := user.EnsureCapacity(ctx, db, ownerID, header.Size); err != nil {
			if errors.Is(err, user.ErrQuotaExceeded) {
				return c.JSON(http.StatusInsufficientStorage, map[string]string{"error": err.Error()})
			}
			return httpx.Fail(c, err, "检查配额失败")
		}

		// 元数据先校验完再落盘：一次注定失败的请求不该白写一遍磁盘。
		parentID, err := parseOptionalUint(c.FormValue("parent_id"), 0)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "非法的 parent_id"})
		}
		if err := checkParent(ctx, db, ownerID, parentID); err != nil {
			return writeParentCheckError(c, err)
		}

		// 校验给出的名字
		name := strings.TrimSpace(c.FormValue("name"))
		if name == "" {
			// 没给就取 header.Filename 的最后一段（因为有可能是路径）
			name = sanitizeUploadName(header.Filename)
		}
		if err := validateName(name); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}

		taken, err := nameTaken(ctx, db, ownerID, parentID, name, 0)
		if err != nil {
			return httpx.Fail(c, err, "检查同名条目失败")
		}
		if taken {
			return c.JSON(http.StatusConflict, map[string]string{"error": errNameTaken.Error()})
		}

		src, err := header.Open()
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "无法读取上传文件"})
		}
		defer src.Close()

		storageKey, size, hash, err := saveUpload(storageDir, src)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "保存文件失败"})
		}

		f := File{
			OwnerID:    ownerID,
			ParentID:   parentID,
			Name:       name,
			Type:       TypeFile,
			Size:       size,
			Hash:       hash,
			StorageKey: storageKey,
			Status:     StatusNormal,
		}
		if err := gorm.G[File](db).Create(ctx, &f); err != nil {
			return httpx.Fail(c, err, "写入文件记录失败")
		}

		// 刷新用量缓存
		if _, err := user.RefreshUsed(ctx, db, ownerID); err != nil {
			c.Logger().Warn("刷新用户用量失败", "err", err, "owner_id", ownerID)
		}

		return c.JSON(http.StatusCreated, f)
	}
}

func download(db *gorm.DB, storageDir string) echo.HandlerFunc {
	return func(c *echo.Context) error {
		ctx := c.Request().Context()
		ownerID := auth.UserID(c)

		id, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "非法的文件 id"})
		}

		f, err := gorm.G[File](db).Where("id = ? AND owner_id = ?", id, ownerID).First(ctx)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return c.JSON(http.StatusNotFound, map[string]string{"error": "文件不存在"})
			}
			return httpx.Fail(c, err, "查询文件失败")
		}
		// 目录、回收站里的文件都不提供下载。
		if f.Type != TypeFile || f.Status != StatusNormal || f.StorageKey == "" {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "文件不可用"})
		}
		return ServeObject(c, storageDir, f)
	}
}

// 打包下载一个目录（流式压缩）
func zipDir(db *gorm.DB, storageDir string) echo.HandlerFunc {
	return func(c *echo.Context) error {
		ctx := c.Request().Context()
		ownerID := auth.UserID(c)

		id, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "非法的文件 id"})
		}

		dir, err := gorm.G[File](db).Where("id = ? AND owner_id = ?", id, ownerID).First(ctx)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return c.JSON(http.StatusNotFound, map[string]string{"error": "文件不存在"})
			}
			return httpx.Fail(c, err, "查询文件失败")
		}
		if dir.Type != TypeDir || dir.Status != StatusNormal {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "只能打包下载目录"})
		}

		wrote, err := StreamZipDir(c, db, ownerID, dir, storageDir)
		if err != nil {
			if wrote {
				// 响应体已经开始写了，状态码改不了：掐断连接，让客户端明确知道失败，
				// 而不是拿一个残缺的压缩包（详见 StreamZipDir 的注释）。
				c.Logger().Error("打包目录中途失败", "err", err, "dir_id", dir.ID)
				panic(http.ErrAbortHandler)
			}
			return httpx.Fail(c, err, "打包目录失败")
		}
		return nil
	}
}

// ---------列表，创建文件夹-----------

// 用指针可以区分"没传"和"传了零值"
// - ParentID 为 nil： 该用户的全部文件；为 0：只看根目录；
// - Status 为 nil： 只看正常文件（1），传2就是看回收站。
type listRequest struct {
	ParentID *uint64 `json:"parent_id"`
	Status   *int8   `json:"status"`
	Limit    uint64  `json:"limit"`  // 0 = 用默认值（200）
	Offset   uint64  `json:"offset"` // 0 = 从头开始
}

func list(db *gorm.DB) echo.HandlerFunc {
	return func(c *echo.Context) error {
		ctx := c.Request().Context()
		ownerID := auth.UserID(c)

		// 请求体可以整个省略，也可以只写关心的字段。
		var req listRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "请求体解析失败"})
		}

		q := gorm.G[File](db).Where("owner_id = ?", ownerID)

		if req.ParentID != nil {
			q = q.Where("parent_id = ?", *req.ParentID)
		}

		status := StatusNormal
		if req.Status != nil {
			if *req.Status < StatusNormal || *req.Status > StatusTrash {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": "非法的 status"})
			}
			status = *req.Status
		}
		q = q.Where("status = ?", status)

		limit := req.Limit
		if limit == 0 {
			limit = defaultPageSize
		}
		if limit > maxPageSize {
			limit = maxPageSize
		}

		items, err := q.Order("type DESC, name ASC"). // 目录(2) 排在文件(1) 前面
								Limit(int(limit)).
								Offset(int(req.Offset)).
								Find(ctx)
		if err != nil {
			return httpx.Fail(c, err, "查询文件列表失败")
		}

		return c.JSON(http.StatusOK, map[string]any{
			"items":  items, // 本页数据
			"count":  len(items),
			"limit":  limit,
			"offset": req.Offset,
		})
	}
}

type createDirRequest struct {
	Name     string `json:"name"`
	ParentID uint64 `json:"parent_id"`
}

func createDir(db *gorm.DB) echo.HandlerFunc {
	return func(c *echo.Context) error {
		ctx := c.Request().Context()
		ownerID := auth.UserID(c)

		var req createDirRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "请求体解析失败"})
		}
		req.Name = strings.TrimSpace(req.Name)
		if err := validateName(req.Name); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}

		if err := checkParent(ctx, db, ownerID, req.ParentID); err != nil {
			return writeParentCheckError(c, err)
		}

		taken, err := nameTaken(ctx, db, ownerID, req.ParentID, req.Name, 0)
		if err != nil {
			return httpx.Fail(c, err, "检查同名条目失败")
		}
		if taken {
			return c.JSON(http.StatusConflict, map[string]string{"error": errNameTaken.Error()})
		}

		f := File{
			Name:     req.Name,
			Type:     TypeDir,
			Status:   StatusNormal,
			ParentID: req.ParentID,
			OwnerID:  ownerID,
		}
		if err := gorm.G[File](db).Create(ctx, &f); err != nil {
			return httpx.Fail(c, err, "创建目录失败")
		}
		return c.JSON(http.StatusCreated, f)
	}
}

// ---------删除恢复-----------

func remove(db *gorm.DB, storageDir string) echo.HandlerFunc {
	return func(c *echo.Context) error {
		ctx := c.Request().Context()
		ownerID := auth.UserID(c)

		fileid, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "非法的文件 id"})
		}

		file, err := gorm.G[File](db).Where("id = ? AND owner_id = ?", fileid, ownerID).First(ctx)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return c.JSON(http.StatusNotFound, map[string]string{"error": "文件不存在"})
			}
			return httpx.Fail(c, err, "查询文件失败")
		}

		// ?permanent=true 是彻底删除
		if c.QueryParam("permanent") == "true" {
			if err := purgeTree(ctx, db, ownerID, file, storageDir); err != nil {
				return httpx.Fail(c, err, "彻底删除失败")
			}
			return c.NoContent(http.StatusNoContent)
		}

		if file.Status == StatusTrash {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "文件已在回收站"})
		}
		if err := removefile(ctx, db, ownerID, fileid); err != nil {
			return httpx.Fail(c, err, "删除文件失败")
		}

		return c.JSON(http.StatusOK, map[string]string{"message": "文件已移入回收站"})
	}
}

// 把回收站里的条目恢复回正常状态；目录会连同子项一起恢复。
func restore(db *gorm.DB) echo.HandlerFunc {
	return func(c *echo.Context) error {
		ctx := c.Request().Context()
		ownerID := auth.UserID(c)

		fileid, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "非法的文件 id"})
		}

		file, err := gorm.G[File](db).Where("id = ? AND owner_id = ?", fileid, ownerID).First(ctx)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return c.JSON(http.StatusNotFound, map[string]string{"error": "文件不存在"})
			}
			return httpx.Fail(c, err, "查询文件失败")
		}
		if file.Status != StatusTrash {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "文件不在回收站"})
		}

		// 上级目录还在回收站时不允许恢复
		if file.ParentID != 0 {
			parent, err := gorm.G[File](db).
				Where("id = ? AND owner_id = ?", file.ParentID, ownerID).First(ctx)
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return c.JSON(http.StatusConflict, map[string]string{"error": "上级目录不存在"})
				}
				return httpx.Fail(c, err, "查询上级目录失败")
			}
			if parent.Status != StatusNormal {
				return c.JSON(http.StatusConflict, map[string]string{"error": "上级目录还在回收站，请先恢复它"})
			}
		}

		taken, err := nameTaken(ctx, db, ownerID, file.ParentID, file.Name, 0)
		if err != nil {
			return httpx.Fail(c, err, "检查同名条目失败")
		}
		if taken {
			return c.JSON(http.StatusConflict, map[string]string{
				"error": "同级下已存在同名条目，请先重命名或删除它再恢复",
			})
		}

		restored, err := restoreTree(ctx, db, ownerID, file.ID)
		if err != nil {
			return httpx.Fail(c, err, "恢复文件失败")
		}
		return c.JSON(http.StatusOK, map[string]any{
			"message":  "已恢复",
			"restored": restored, // 含子项
		})
	}
}

// ---------改名移动-----------

type updateRequest struct {
	Name     *string `json:"name"`
	ParentID *uint64 `json:"parent_id"`
}

func update(db *gorm.DB) echo.HandlerFunc {
	return func(c *echo.Context) error {
		ctx := c.Request().Context()
		ownerID := auth.UserID(c)

		fileid, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "非法的文件 id"})
		}

		var req updateRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "请求体解析失败"})
		}
		if req.Name == nil && req.ParentID == nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "没有要修改的字段"})
		}

		newName := ""
		if req.Name != nil {
			newName = strings.TrimSpace(*req.Name)
			if err := validateName(newName); err != nil {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
			}
		}

		file, err := gorm.G[File](db).Where("id = ? AND owner_id = ?", fileid, ownerID).First(ctx)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return c.JSON(http.StatusNotFound, map[string]string{"error": "文件不存在"})
			}
			return httpx.Fail(c, err, "查询文件失败")
		}

		// 没传的字段保持原值。
		if req.Name == nil {
			newName = file.Name
		}
		newParentID := file.ParentID
		if req.ParentID != nil {
			newParentID = *req.ParentID
		}

		// 位置变了才需要校验移动目标（移到原目录属于无操作，直接放行）。
		if newParentID != file.ParentID {
			if err := checkMoveTarget(ctx, db, ownerID, file, newParentID); err != nil {
				return writeMoveError(c, err)
			}
		}

		// 重名检查必须排除自己
		taken, err := nameTaken(ctx, db, ownerID, newParentID, newName, file.ID)
		if err != nil {
			return httpx.Fail(c, err, "检查同名条目失败")
		}
		if taken {
			return c.JSON(http.StatusConflict, map[string]string{"error": errNameTaken.Error()})
		}

		// 用 map 更新，要写零值
		if _, err := gorm.G[map[string]any](db).Table("files").
			Where("id = ? AND owner_id = ?", file.ID, ownerID).
			Updates(ctx, map[string]any{"name": newName, "parent_id": newParentID}); err != nil {
			return httpx.Fail(c, err, "更新文件失败")
		}

		file.Name = newName
		file.ParentID = newParentID
		return c.JSON(http.StatusOK, map[string]any{
			"message": "已更新",
			"file":    file,
		})
	}
}
