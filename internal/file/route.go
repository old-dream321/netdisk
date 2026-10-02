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

	"netdisk/pkg/httpx"
)

const (
	// defaultOwnerID 鉴权尚未接入，未传 owner_id 时的兜底归属。
	defaultOwnerID = 1
	// maxUploadSize 单文件大小上限 1GiB。
	maxUploadSize = 1 << 30
	// 列表默认返回条数与单次上限，避免一次把整个网盘拉出来。
	defaultPageSize = 200
	maxPageSize     = 1000
)

func RegisterRoutes(g *echo.Group, db *gorm.DB) {
	g.GET("", list(db))
	g.POST("/upload", upload(db))
	g.POST("/createdir", createDir(db))
	g.GET("/:id/download", download(db))
	g.DELETE("/:id", remove(db))
}

func upload(db *gorm.DB) echo.HandlerFunc {
	return func(c *echo.Context) error {
		ctx := c.Request().Context()

		header, err := c.FormFile("file")
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "缺少上传文件字段 file"})
		}
		if header.Size > maxUploadSize {
			return c.JSON(http.StatusRequestEntityTooLarge, map[string]string{"error": "文件超过大小限制"})
		}

		// 元数据先校验完再落盘：一次注定失败的请求不该白写一遍磁盘。
		ownerID, err := parseOptionalUint(c.FormValue("owner_id"), defaultOwnerID)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "非法的 owner_id"})
		}
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

// parseOptionalUint 解析"引用型"的可选整数（owner_id / parent_id / status）：
// 缺省时返回 fallback，填了但不合法则返回错误。这类值静默回退很危险——
// parent_id 写错会被塞进根目录，owner_id 写错会归属到别人名下。
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

// checkParent 校验 parentID 是否可作为 ownerID 下新条目的父级。
// parentID 为 0 表示根目录，直接通过；非 0 时该记录必须存在、
// 属于同一 owner、是目录(而非文件)、且状态正常。
//
// ctx 之所以当第一个参数传进来（而不是存进某个结构体），是 Go 的官方约定：
// 取消信号与超时必须沿调用链传递，谁都不能私自"记住"它。
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

func download(db *gorm.DB) echo.HandlerFunc {
	return func(c *echo.Context) error {
		ctx := c.Request().Context()

		id, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "非法的文件 id"})
		}

		// 泛型 First 直接返回 (T, error)，不用先声明变量再传指针进去。
		// 查不到时错误仍是 gorm.ErrRecordNotFound，判断方式不变。
		f, err := gorm.G[File](db).Where("id = ?", id).First(ctx)
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

		ownerID, err := parseOptionalUint(c.QueryParam("owner_id"), defaultOwnerID)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "非法的 owner_id"})
		}

		// gorm.G[File](db) 已经把模型类型固定住了，不用再写 Model(&File{})；
		// 这里的 Where 返回 ChainInterface[File]，所以后续链式调用都必须
		// 赋回同一个变量 q（GORM 的不可变链式，和经典 API 一致）。
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
	OwnerID  uint64 `json:"owner_id"`
}

func createDir(db *gorm.DB) echo.HandlerFunc {
	return func(c *echo.Context) error {
		ctx := c.Request().Context()

		var req createDirRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "请求体解析失败"})
		}
		req.Name = strings.TrimSpace(req.Name)
		if req.Name == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "目录名不能为空"})
		}

		ownerID := req.OwnerID
		if ownerID == 0 {
			ownerID = defaultOwnerID
		}
		if err := checkParent(ctx, db, ownerID, req.ParentID); err != nil {
			return writeParentCheckError(c, err)
		}

		// 同级不允许重名：否则列表里会出现一堆看不出区别的同名目录。
		// 把文件也算进来，避免"同名文件和目录共存"这种更麻烦的情况。
		// 泛型 Count 返回 (int64, error)，不用再传 &sameName 进去。
		sameName, err := gorm.G[File](db).
			Where("owner_id = ? AND parent_id = ? AND name = ? AND status <> ?",
				ownerID, req.ParentID, req.Name, StatusDeleted).
			Count(ctx, "*")
		if err != nil {
			return httpx.Fail(c, err, "检查同名条目失败")
		}
		if sameName > 0 {
			return c.JSON(http.StatusConflict, map[string]string{"error": "同级下已存在同名文件或目录"})
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

func remove(_ *gorm.DB) echo.HandlerFunc {
	return func(c *echo.Context) error {
		return c.JSON(http.StatusNotImplemented, map[string]string{"error": "删除接口尚未实现"})
	}
}
