package web

import (
	"context"
	"embed"

	"github.com/Struck713/brakarr/internal/config"
	"github.com/Struck713/brakarr/internal/pool"
	"github.com/Struck713/brakarr/internal/web/controllers"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

//go:embed static
var staticFiles embed.FS

// Controller is implemented by anything that can register its routes on the router.
type Controller interface {
	Register(e *echo.Echo)
}

// Web holds the HTTP controllers that make up the web interface.
type Web struct {
	controllers []Controller
}

// Store is the persistence the web controllers read from and update.
type Store interface {
	controllers.Store
	controllers.HistoryStore
}

// NewWeb builds the web interface with its library, activity, settings and services controllers.
func NewWeb(db Store, pool *pool.Pool, settings *config.Store, runtime controllers.Runtime) *Web {
	return &Web{controllers: []Controller{
		controllers.NewLibraryController(db, pool),
		controllers.NewActivityController(db, pool),
		controllers.NewSettingsController(settings, runtime),
		controllers.NewServicesController(settings),
	}}
}

// Routes builds the echo router with middleware, static files and controller routes.
func (w *Web) Routes() *echo.Echo {
	e := echo.New()

	e.Use(middleware.RequestLogger())
	e.Use(middleware.Recover())

	e.StaticFS("/static", echo.MustSubFS(staticFiles, "static"))

	for _, c := range w.controllers {
		c.Register(e)
	}
	return e
}

// Run serves HTTP on addr until ctx is cancelled.
func (w *Web) Run(ctx context.Context, addr string) error {
	return echo.StartConfig{Address: addr}.Start(ctx, w.Routes())
}
