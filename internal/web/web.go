package web

import (
	"net/http"

	"github.com/Struck713/brakarr/internal/database"
	"github.com/Struck713/brakarr/internal/pool"
	"github.com/Struck713/brakarr/internal/web/templates"
	"github.com/a-h/templ"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

type Web struct {
	db   *database.Database
	pool *pool.Pool
}

func NewWeb(db *database.Database, pool *pool.Pool) *Web {
	return &Web{db: db, pool: pool}
}

func Render(c *echo.Context, statusCode int, t templ.Component) error {
	c.Response().WriteHeader(statusCode)
	c.Response().Header().Set(echo.HeaderContentType, echo.MIMETextHTMLCharsetUTF8)
	return t.Render(c.Request().Context(), c.Response())
}

func (w *Web) Init() {
	e := echo.New()

	e.Use(middleware.RequestLogger())
	e.Use(middleware.Recover())

	e.GET("/", func(c *echo.Context) error {
		return Render(c, http.StatusOK, templates.Index())
	})

	e.GET("/crawler", func(c *echo.Context) error {
		records, err := w.db.All(c.Request().Context())
		if err != nil {
			return err
		}
		return Render(c, http.StatusOK, templates.Table(records))
	})

	e.GET("/job", func(c *echo.Context) error {
		jobs := w.pool.Jobs()
		return Render(c, http.StatusOK, templates.ActiveJobs(jobs))
	})

	if err := e.Start(":1323"); err != nil {
		e.Logger.Error("failed to start server", "error", err)
	}
}
