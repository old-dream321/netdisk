package file

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/labstack/echo/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"netdisk/internal/auth"
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

func RegisterRoutes(g *echo.Group, db *gorm.DB, sessions session.Store) {
	// 整个 files 组都要求登录：挂在组上，组里新增的接口自动受保护。
	g = g.Group("", auth.RequireLogin(sessions))

	g.GET("", list(db))
	g.POST("/upload", upload(db))
	g.POST("/createdir", createDir(db))
	g.GET("/:id/download", download(db))
	g.PATCH("/:id", update(db))         // 改名 / 移动
	g.DELETE("/:id", remove(db))        // 默认软删除（进回收站），?permanent=true 为彻底删除
	g.POST("/:id/restore", restore(db)) // 从回收站恢复（目录连同子项）
}

func upload(db *gorm.DB) echo.HandlerFunc {
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

		// 同级重名检查，和 createdir 用同一套规则。
		// 放在落盘之前：注定失败的上传不该先写一遍磁盘。
		// 新条目还没有 id，excludeID 传 0。
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
		storageKey, size, hash, err := saveUpload(src)
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

		return c.JSON(http.StatusCreated, f)
	}
}

// parseOptionalUint 解析"引用型"的可选整数（parent_id / status）：
// 缺省时返回 fallback，填了但不合法则返回错误。这类值静默回退很危险——
// parent_id 写错会被塞进根目录，用户完全察觉不到。
func parseOptionalUint(raw string, fallback uint64) (uint64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, nil
	}
	return strconv.ParseUint(raw, 10, 64)
}

// parseUintOr 解析"数值旋钮"（limit / offset）：不合法就用 fallback，
// 为了一个分页参数报错、让前端多写一堆错误处理，不值得。
func parseUintOr(raw string, fallback uint64) uint64 {
	v, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return fallback
	}
	return v
}

// errParentInvalid 是父目录校验失败的统一错误。
// "不存在""不属于你""不是目录""已删除"故意共用一句话：
// 若分别提示，就等于向调用方泄露了别人目录的存在性。
var errParentInvalid = errors.New("父目录不存在或不是可用目录")

func checkParent(ctx context.Context, db *gorm.DB, ownerID, parentID uint64) error {
	if parentID == 0 {
		return nil
	}

	parent, err := gorm.G[File](db).Where("id = ?", parentID).First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errParentInvalid
		}
		return fmt.Errorf("查询父目录: %w", err)
	}
	if parent.OwnerID != ownerID || parent.Type != TypeDir || parent.Status != StatusNormal {
		return errParentInvalid
	}
	return nil
}

// writeParentCheckError 把 checkParent 的错误翻成响应：
// 校验不通过是调用方的问题(400)，查询本身失败是服务端的锅(500)。
func writeParentCheckError(c *echo.Context, err error) error {
	if errors.Is(err, errParentInvalid) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	return httpx.Fail(c, err, "校验父目录失败")
}

// nameTaken 判断同级下是否已经存在"正常状态"的同名条目。
//
// 同名规则：只有 status=正常 的条目占名字。
// 回收站里的（status=2）不占——用户删掉一个东西之后，应该能马上重建同名；
// 已彻底删除（status=3）的记录根本不存在，自然也不占。
// 恢复（restore）时再单独校验一次，因为此时名字可能已经被新条目用掉了。
//
// excludeID 用来把自己排除掉：改名/移动时"保持原来的名字或位置"不该算冲突，
// 否则把 a.txt 改名成 a.txt、或把文件移到它现在所在的目录，都会被误判成重名。
// 新建条目（还没有 id）传 0。
func nameTaken(ctx context.Context, db *gorm.DB, ownerID, parentID uint64, name string, excludeID uint64) (bool, error) {
	q := gorm.G[File](db).
		Where("owner_id = ? AND parent_id = ? AND name = ? AND status = ?",
			ownerID, parentID, name, StatusNormal)
	if excludeID != 0 {
		q = q.Where("id <> ?", excludeID)
	}

	n, err := q.Count(ctx, "*")
	if err != nil {
		return false, fmt.Errorf("检查同名条目: %w", err)
	}
	return n > 0, nil
}

// errNameTaken 是同名冲突的统一错误，upload / createdir / restore / update 共用。
var errNameTaken = errors.New("同级下已存在同名文件或目录")

func download(db *gorm.DB) echo.HandlerFunc {
	return func(c *echo.Context) error {
		ctx := c.Request().Context()
		ownerID := auth.UserID(c)

		id, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "非法的文件 id"})
		}

		// 归属条件直接写进 WHERE：别人的文件一律"查不到"。
		// 不用 403 区分，是为了不泄露"这个 id 确实存在"。
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

		src, err := os.Open(objectPath(f.StorageKey))
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "读取文件内容失败"})
		}
		defer src.Close()

		info, err := src.Stat()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "读取文件信息失败"})
		}

		h := c.Response().Header()
		if cd := contentDisposition(f.Name); cd != "" {
			h.Set(echo.HeaderContentDisposition, cd)
		}
		// ServeContent 自带 Content-Type 探测、Content-Length、
		// Last-Modified 和 Range 支持（断点续传/视频拖动进度条都靠它）。
		http.ServeContent(c.Response(), c.Request(), f.Name, info.ModTime(), src)
		return nil
	}
}

func list(db *gorm.DB) echo.HandlerFunc {
	return func(c *echo.Context) error {
		ctx := c.Request().Context()
		ownerID := auth.UserID(c)

		q := gorm.G[File](db).Where("owner_id = ?", ownerID)

		// 不带 parent_id 表示"该用户的全部文件"；带上就只看这个目录的直接子项。
		if raw := strings.TrimSpace(c.QueryParam("parent_id")); raw != "" {
			parentID, err := parseOptionalUint(raw, 0)
			if err != nil {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": "非法的 parent_id"})
			}
			q = q.Where("parent_id = ?", parentID)
		}

		// 默认只列正常文件，status=2 可以查看回收站。
		status := StatusNormal
		if raw := strings.TrimSpace(c.QueryParam("status")); raw != "" {
			v, err := parseOptionalUint(raw, uint64(StatusNormal))
			if err != nil || v > uint64(StatusDeleted) {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": "非法的 status"})
			}
			status = int8(v)
		}
		q = q.Where("status = ?", status)

		limit := parseUintOr(c.QueryParam("limit"), defaultPageSize)
		if limit == 0 {
			limit = defaultPageSize
		}
		if limit > maxPageSize {
			limit = maxPageSize
		}
		offset := parseUintOr(c.QueryParam("offset"), 0)

		// 泛型 Find 直接返回 []File。GORM 在 scan 阶段会把目标切片
		// 初始化成长度 0 的非 nil 切片，所以查不到数据时序列化出来就是
		// []，不需要自己 make（实测确认过）。
		items, err := q.Order("type DESC, name ASC"). // 目录(2) 排在文件(1) 前面
								Limit(int(limit)).
								Offset(int(offset)).
								Find(ctx)
		if err != nil {
			return httpx.Fail(c, err, "查询文件列表失败")
		}

		return c.JSON(http.StatusOK, map[string]any{
			"items":  items, // 本页数据
			"count":  len(items),
			"limit":  limit,
			"offset": offset,
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
		Update(ctx, "status", file.Status); err != nil {
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
		Update(ctx, "status", StatusNormal)
	if err != nil {
		return 0, fmt.Errorf("恢复状态: %w", err)
	}
	return n, nil
}

// purgeTree 彻底删除 id 及其子孙：先删数据库记录，再清理不再被引用的磁盘对象。
//
// 顺序很重要。对象文件的操作不在数据库事务里：
//   - 先删记录、后清对象：中途失败只会留下孤儿对象文件（无害，以后还能清）；
//   - 反过来先删对象：一旦事务回滚，就留下"记录还在、内容没了"的坏数据。
func purgeTree(ctx context.Context, db *gorm.DB, ownerID uint64, root File) error {
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
		if err := os.Remove(objectPath(key)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("删除对象文件: %w", err)
		}
	}
	return nil
}

func remove(db *gorm.DB) echo.HandlerFunc {
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
			if err := purgeTree(ctx, db, ownerID, file); err != nil {
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

// errMoveToSelf / errMoveToDescendant 是两类会把目录树弄坏的移动。
var (
	errMoveToSelf       = errors.New("不能把条目移动到它自己里面")
	errMoveToDescendant = errors.New("不能把目录移动到它自己的子目录里")
)

// checkMoveTarget 校验 newParentID 能不能作为 file 的新位置。
// checkParent 负责"目标目录本身是否可用"，这里额外拦两类会造成环的移动——
// 一旦成环（A 在 B 里、B 又在 A 里），从列表就再也走不到这些记录，
// 而 collectSubtree 之类的遍历会死循环。
func checkMoveTarget(ctx context.Context, db *gorm.DB, ownerID uint64, file File, newParentID uint64) error {
	if err := checkParent(ctx, db, ownerID, newParentID); err != nil {
		return err
	}
	if newParentID == file.ID {
		return errMoveToSelf
	}
	if file.Type != TypeDir {
		return nil // 文件没有子孙，不可能成环
	}

	descendants, err := collectSubtree(ctx, db, ownerID, file.ID)
	if err != nil {
		return err
	}
	for _, d := range descendants {
		if d.ID == newParentID {
			return errMoveToDescendant
		}
	}
	return nil
}

// writeMoveError 把移动目标的校验错误翻成响应：都是调用方输入的问题(400)，
// 查询本身失败才是服务端的锅(500)。
func writeMoveError(c *echo.Context, err error) error {
	if errors.Is(err, errParentInvalid) || errors.Is(err, errMoveToSelf) || errors.Is(err, errMoveToDescendant) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	return httpx.Fail(c, err, "校验移动目标失败")
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
