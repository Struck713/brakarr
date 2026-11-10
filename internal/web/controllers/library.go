package controllers

import (
	"context"
	"encoding/hex"
	"net/http"
	"strconv"

	"github.com/Struck713/brakarr/internal/media"
	"github.com/Struck713/brakarr/internal/pool"
	"github.com/Struck713/brakarr/internal/web/templates"
	"github.com/labstack/echo/v5"
)

const pageSize = 50

type Store interface {
	List(ctx context.Context, limit, offset int) ([]media.Media, error)
	Count(ctx context.Context) (int, error)
	Retry(ctx context.Context, hash []byte) (bool, error)
}

type LibraryController struct {
	db   Store
	pool *pool.Pool
}

func NewLibraryController(db Store, pool *pool.Pool) *LibraryController {
	return &LibraryController{db: db, pool: pool}
}

func (l *LibraryController) Register(e *echo.Echo) {
	e.GET("/", l.Index)
	e.GET("/media", l.Panel)
	e.POST("/media/:hash/retry", l.Retry)
}

func (l *LibraryController) Index(c *echo.Context) error {
	list, err := l.list(c, pageParam(c))
	if err != nil {
		return err
	}
	return Render(c, http.StatusOK, templates.LibraryPage(list))
}

func (l *LibraryController) Panel(c *echo.Context) error {
	return l.panel(c)
}

func (l *LibraryController) Retry(c *echo.Context) error {
	hash, err := hex.DecodeString(c.Param("hash"))
	if err != nil || len(hash) == 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid hash")
	}
	if _, err := l.db.Retry(c.Request().Context(), hash); err != nil {
		return err
	}
	l.pool.Notify()
	return l.panel(c)
}

func (l *LibraryController) panel(c *echo.Context) error {
	list, err := l.list(c, pageParam(c))
	if err != nil {
		return err
	}
	return Render(c, http.StatusOK, templates.MediaPanel(list))
}

func (l *LibraryController) list(c *echo.Context, page int) (templates.MediaList, error) {
	ctx := c.Request().Context()

	total, err := l.db.Count(ctx)
	if err != nil {
		return templates.MediaList{}, err
	}
	pages := max((total+pageSize-1)/pageSize, 1)
	page = min(max(page, 1), pages)

	items, err := l.db.List(ctx, pageSize, (page-1)*pageSize)
	if err != nil {
		return templates.MediaList{}, err
	}
	return templates.MediaList{Items: items, Page: page, Pages: pages, Total: total}, nil
}

func pageParam(c *echo.Context) int {
	n, err := strconv.Atoi(c.QueryParam("page"))
	if err != nil {
		return 1
	}
	return n
}
