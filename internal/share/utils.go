package share

import (
	"crypto/rand"
	"net/http"
	"netdisk/internal/file"
	"netdisk/pkg/httpx"
	"strconv"

	"github.com/labstack/echo/v5"
	"gorm.io/gorm"
)

func newToken() string {
	return rand.Text()
}

// 列出目录内容
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
