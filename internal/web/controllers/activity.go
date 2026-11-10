package controllers

import (
	"cmp"
	"net/http"
	"slices"

	"github.com/Struck713/brakarr/internal/pool"
	"github.com/Struck713/brakarr/internal/web/templates"
	"github.com/labstack/echo/v5"
)

type ActivityController struct {
	pool *pool.Pool
}

func NewActivityController(pool *pool.Pool) *ActivityController {
	return &ActivityController{pool: pool}
}

func (a *ActivityController) Register(e *echo.Echo) {
	e.GET("/activity", a.Index)
	e.GET("/jobs", a.Jobs)
}

func (a *ActivityController) Index(c *echo.Context) error {
	return Render(c, http.StatusOK, templates.ActivityPage(a.active()))
}

func (a *ActivityController) Jobs(c *echo.Context) error {
	return Render(c, http.StatusOK, templates.ActiveJobs(a.active()))
}

func (a *ActivityController) active() []*pool.Job {
	jobs := a.pool.Jobs()
	slices.SortFunc(jobs, func(x, y *pool.Job) int {
		return cmp.Compare(x.Media.Name, y.Media.Name)
	})
	return jobs
}
