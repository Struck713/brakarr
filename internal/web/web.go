package web

import (
	"context"
	"encoding/hex"
	"net/http"
	"strconv"

	"github.com/Struck713/brakarr/internal/media"
	"github.com/Struck713/brakarr/internal/pool"
	"github.com/Struck713/brakarr/internal/web/templates"
	"github.com/a-h/templ"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

const pageSize = 50

type Store interface {
	List(ctx context.Context, limit, offset int) ([]media.Media, error)
	Count(ctx context.Context) (int, error)
	Retry(ctx context.Context, hash []byte) (bool, error)
}

type Web struct {
	db   Store
	pool *pool.Pool
}

func NewWeb(db Store, pool *pool.Pool) *Web {
	return &Web{db: db, pool: pool}
}

func Render(c *echo.Context, statusCode int, t templ.Component) error {
	c.Response().Header().Set(echo.HeaderContentType, echo.MIMETextHTMLCharsetUTF8)
	c.Response().WriteHeader(statusCode)
	return t.Render(c.Request().Context(), c.Response())
}

func (w *Web) Routes() *echo.Echo {
	e := echo.New()

	e.Use(middleware.RequestLogger())
	e.Use(middleware.Recover())

	e.GET("/", func(c *echo.Context) error {
		return Render(c, http.StatusOK, templates.Index())
	})

	e.GET("/media", func(c *echo.Context) error {
		return w.mediaPanel(c, pageParam(c))
	})

	e.POST("/media/:hash/retry", func(c *echo.Context) error {
		hash, err := hex.DecodeString(c.Param("hash"))
		if err != nil || len(hash) == 0 {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid hash")
		}
		if _, err := w.db.Retry(c.Request().Context(), hash); err != nil {
			return err
		}
		w.pool.Notify()
		return w.mediaPanel(c, pageParam(c))
	})

	e.GET("/jobs", func(c *echo.Context) error {
		return Render(c, http.StatusOK, templates.ActiveJobs(w.pool.Jobs()))
	})

	return e
}

func (w *Web) Run(ctx context.Context, addr string) error {
	return echo.StartConfig{Address: addr}.Start(ctx, w.Routes())
}

func (w *Web) mediaPanel(c *echo.Context, page int) error {
	ctx := c.Request().Context()

	total, err := w.db.Count(ctx)
	if err != nil {
		return err
	}
	pages := max((total+pageSize-1)/pageSize, 1)
	page = min(max(page, 1), pages)

	items, err := w.db.List(ctx, pageSize, (page-1)*pageSize)
	if err != nil {
		return err
	}
	return Render(c, http.StatusOK, templates.MediaPanel(items, page, pages, total))
}

func pageParam(c *echo.Context) int {
	n, err := strconv.Atoi(c.QueryParam("page"))
	if err != nil {
		return 1
	}
	return n
}
