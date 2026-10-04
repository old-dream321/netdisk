package file

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"netdisk/internal/auth"
	"netdisk/internal/user"
	"netdisk/pkg/httpx"
	"netdisk/pkg/session"
)

const (
	// maxUploadSize 单文件大小上限 1GiB。
	maxUploadSize = 1 << 30
	// 列表默认返回条数与单次上限，避免一次把整个网盘拉出来。
	defaultPageSize = 200
	maxPageSize     = 1000
)

func RegisterRoutes(g *echo.Group, db *gorm.DB, sessions session.Store, storageDir string) {
	g = g.Group("", auth.RequireLogin(sessions))

	g.POST("/list", list(db)) // 列表：POST + JSON body
	g.POST("/upload", upload(db, storageDir))
	g.POST("/createdir", createDir(db))
	g.GET("/:id/download", download(db, storageDir))
	// HEAD 和 GET 共用同一个 handler。http.ServeContent 自己认得 HEAD，很神奇（
	g.HEAD("/:id/download", download(db, storageDir))
	g.PATCH("/:id", update(db))              // 改名 / 移动
	g.DELETE("/:id", remove(db, storageDir)) // 默认软删除（进回收站），?permanent=true 为彻底删除
	g.POST("/:id/restore", restore(db))      // 从回收站恢复
}

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

		// 配额预检查放在最前面：此刻还没落盘，被拒时不需要清理任何东西。
		// 用 header.Size（multipart 解析出来的真实长度）而不是客户端报的数字。
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

		name := strings.TrimSpace(c.FormValue("name"))
		if name == "" {
			name = header.Filename
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

		// 内容先落盘，再做记录，避免出现「有记录没文件」。
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

		// 刷新用量缓存（/me 显示的数字）。失败只影响展示，不该让上传失败。
		if _, err := user.RefreshUsed(ctx, db, ownerID); err != nil {
			slog.Warn("刷新用户用量失败", "err", err, "owner_id", ownerID)
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

// listRequest 是列表的查询条件。
//
// 用指针是为了区分"没传"和"传了零值"：
//   - ParentID 为 nil = 该用户的全部文件；为 0 = 只看根目录；
//   - Status 为 nil = 只看正常文件（1），传 2 就是看回收站。
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

		// gorm.G[File](db) 已经把模型类型固定住了，不用再写 Model(&File{})；
		// 这里的 Where 返回 ChainInterface[File]，所以后续链式调用都必须
		// 赋回同一个变量 q（GORM 的不可变链式，和经典 API 一致）。
		q := gorm.G[File](db).Where("owner_id = ?", ownerID)

		if req.ParentID != nil {
			q = q.Where("parent_id = ?", *req.ParentID)
		}

		status := StatusNormal
		if req.Status != nil {
			if *req.Status < StatusNormal || *req.Status > StatusDeleted {
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
		if req.Name == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "目录名不能为空"})
		}

		if err := checkParent(ctx, db, ownerID, req.ParentID); err != nil {
			return writeParentCheckError(c, err)
		}

		// 同级不允许重名（把文件也算进来，避免"同名文件和目录共存"）。
		// 具体规则见 nameTaken：回收站里的条目不占名字。
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

func removefile(ctx context.Context, db *gorm.DB, fileid uint64) error {
	file, err := gorm.G[File](db).Where("id = ?", fileid).First(ctx)
	if err != nil {
		return err
	}
	if file.Status == StatusTrash || file.Status == StatusDeleted {
		return nil // 已经在回收站或已删除的文件不再处理
	}
	if file.Type == TypeDir {
		// 目录要递归删除：先查出所有子项，再逐个删。
		children, err := gorm.G[File](db).Where("parent_id = ?", fileid).Find(ctx)
		if err != nil {
			return fmt.Errorf("查询子项: %w", err)
		}
		for _, child := range children {
			if err := removefile(ctx, db, child.ID); err != nil {
				return fmt.Errorf("删除子项 %d: %w", child.ID, err)
			}
		}
	}

	file.Status = StatusTrash
	if _, err := gorm.G[File](db).
		Where("id = ?", fileid).
		Set(clause.Assignments(map[string]any{"status": file.Status, "deleted_at": time.Now()})).
		Update(ctx); err != nil {
		return fmt.Errorf("更新状态: %w", err)
	}
	return nil
}

// collectSubtree 返回 rootID 下的所有子孙（不含 rootID 自己）。
// 只走 ownerID 自己的记录：这里的结果会被用于不可逆的操作，
// 不能靠"父子记录的 owner 应该一致"这种推断来兜底。
// 用队列做广度优先而不是递归，避免目录层级极深时把调用栈压爆。
func collectSubtree(ctx context.Context, db *gorm.DB, ownerID, rootID uint64) ([]File, error) {
	var out []File
	queue := []uint64{rootID}

	for i := 0; i < len(queue); i++ {
		children, err := gorm.G[File](db).
			Where("parent_id = ? AND owner_id = ?", queue[i], ownerID).
			Find(ctx)
		if err != nil {
			return nil, fmt.Errorf("查询 id=%d 的子项: %w", queue[i], err)
		}
		for _, child := range children {
			out = append(out, child)
			if child.Type == TypeDir {
				queue = append(queue, child.ID)
			}
		}
	}
	return out, nil
}

// restoreTree 把 id 及所有"还在回收站里"的子孙恢复成正常状态，返回恢复的条数。
// 已经正常的子项不动（它可能是单独恢复过的）；已彻底删除的也不会被复活
// （那种记录早已不存在）。
func restoreTree(ctx context.Context, db *gorm.DB, ownerID, id uint64) (int, error) {
	descendants, err := collectSubtree(ctx, db, ownerID, id)
	if err != nil {
		return 0, err
	}

	ids := make([]uint64, 0, len(descendants)+1)
	ids = append(ids, id)
	for _, f := range descendants {
		ids = append(ids, f.ID)
	}

	n, err := gorm.G[File](db).
		Where("id IN ? AND owner_id = ? AND status = ?", ids, ownerID, StatusTrash).
		Set(clause.Assignments(map[string]any{"status": StatusNormal, "deleted_at": nil})).
		Update(ctx)
	if err != nil {
		return 0, fmt.Errorf("恢复状态: %w", err)
	}
	return n, nil
}

// purgeTree 彻底删除 id 及其子孙：先删数据库记录，再清理不再被引用的磁盘对象。
func purgeTree(ctx context.Context, db *gorm.DB, ownerID uint64, root File, storageDir string) error {
	descendants, err := collectSubtree(ctx, db, ownerID, root.ID)
	if err != nil {
		return err
	}

	ids := make([]uint64, 0, len(descendants)+1)
	ids = append(ids, root.ID)
	keys := make([]string, 0, len(descendants)+1)
	if root.StorageKey != "" {
		keys = append(keys, root.StorageKey)
	}
	for _, f := range descendants {
		ids = append(ids, f.ID)
		if f.StorageKey != "" {
			keys = append(keys, f.StorageKey)
		}
	}

	// 一个 DELETE 干掉整棵子树；条件带 owner_id，删不到别人的记录。
	if _, err := gorm.G[File](db).
		Where("id IN ? AND owner_id = ?", ids, ownerID).
		Delete(ctx); err != nil {
		return fmt.Errorf("删除记录: %w", err)
	}

	// 内容寻址存储让多条记录共享同一个 storage_key（这正是去重的代价），
	// 所以删对象之前必须确认"全表（包括别人的、回收站里的）没有任何记录还引用它"。
	// 只看自己的记录会把别人正在用的文件删掉。
	for _, key := range keys {
		refs, err := gorm.G[File](db).Where("storage_key = ?", key).Count(ctx, "*")
		if err != nil {
			return fmt.Errorf("统计对象引用: %w", err)
		}
		if refs > 0 {
			continue
		}
		if err := os.Remove(ObjectPath(storageDir, key)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("删除对象文件: %w", err)
		}
	}

	// 彻底删除才释放配额（回收站里的还占着），所以刷新一下用量缓存。
	// 记录已经删了，这里失败只影响展示，不该让请求报错。
	if _, err := user.RefreshUsed(ctx, db, ownerID); err != nil {
		slog.Warn("刷新用户用量失败", "err", err, "owner_id", ownerID)
	}
	return nil
}

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

		// ?permanent=true 是彻底删除：记录和磁盘对象一起清掉，不可恢复。
		// 不要求先经过回收站——它就是"直接全删"。
		if c.QueryParam("permanent") == "true" {
			if err := purgeTree(ctx, db, ownerID, file, storageDir); err != nil {
				return httpx.Fail(c, err, "彻底删除失败")
			}
			return c.NoContent(http.StatusNoContent)
		}

		if file.Status == StatusTrash || file.Status == StatusDeleted {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "文件已在回收站"})
		}
		if err := removefile(ctx, db, fileid); err != nil {
			return httpx.Fail(c, err, "删除文件失败")
		}

		return c.JSON(http.StatusOK, map[string]string{"message": "文件已移入回收站"})
	}
}

// restore 把回收站里的条目恢复回正常状态；目录会连同子项一起恢复。
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

		// 上级目录还在回收站时不允许恢复：恢复出来也是一个"看不见"的条目，
		// 用户在列表里根本找不到它。让他先恢复上级目录。
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

		// 回收站里的条目不占名字，所以这个名字可能已经被新条目用掉了，
		// 恢复前必须再查一次，否则会恢复出两个同名的可见条目。
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

type updateRequest struct {
	Name     *string `json:"name"`      // nil = 不改名
	ParentID *uint64 `json:"parent_id"` // nil = 不移动；&0 = 移到根目录
}

// update 处理 PATCH /api/files/:id：改名、移动，或者两者一起。
//
// 两个字段都是可选的，用指针是为了区分"没传"和"传了零值"——
// parent_id=0 是合法且有意义的（移到根目录），不能和"没传"混为一谈。
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
			if newName == "" {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": "新名字不能为空"})
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

		// 重名检查必须排除自己，否则"改成现在这个名字"会误判成冲突。
		taken, err := nameTaken(ctx, db, ownerID, newParentID, newName, file.ID)
		if err != nil {
			return httpx.Fail(c, err, "检查同名条目失败")
		}
		if taken {
			return c.JSON(http.StatusConflict, map[string]string{"error": errNameTaken.Error()})
		}

		if _, err := gorm.G[File](db).
			Where("id = ? AND owner_id = ?", file.ID, ownerID).
			Set(clause.Assignments(map[string]any{"name": newName, "parent_id": newParentID})). // 这个可以写零值
			Update(ctx); err != nil {
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
