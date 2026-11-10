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

// Controller registers its routes on the server.
type Controller interface {
	Register(e *echo.Echo)
}

type Web struct {
	controllers []Controller
}

func NewWeb(db controllers.Store, pool *pool.Pool, settings *config.Store) *Web {
	return &Web{controllers: []Controller{
		controllers.NewLibraryController(db, pool),
		controllers.NewActivityController(pool),
		controllers.NewSettingsController(settings),
	}}
}

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

func (w *Web) Run(ctx context.Context, addr string) error {
	return echo.StartConfig{Address: addr}.Start(ctx, w.Routes())
}
