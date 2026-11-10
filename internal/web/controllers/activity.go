package controllers

import (
	"cmp"
	"net/http"
	"slices"

	"github.com/Struck713/brakarr/internal/pool"
	"github.com/Struck713/brakarr/internal/web/templates"
	"github.com/labstack/echo/v5"
)

// ActivityController serves the activity page showing active transcode jobs.
type ActivityController struct {
	pool *pool.Pool
}

// NewActivityController creates an ActivityController backed by the given pool.
func NewActivityController(pool *pool.Pool) *ActivityController {
	return &ActivityController{pool: pool}
}

// Register adds the activity routes to the router.
func (a *ActivityController) Register(e *echo.Echo) {
	e.GET("/activity", a.Index)
	e.GET("/jobs", a.Jobs)
}

// Index renders the full activity page.
func (a *ActivityController) Index(c *echo.Context) error {
	return Render(c, http.StatusOK, templates.ActivityPage(a.active()))
}

// Jobs renders the list of active jobs as a partial.
func (a *ActivityController) Jobs(c *echo.Context) error {
	return Render(c, http.StatusOK, templates.ActiveJobs(a.active()))
}

// active returns the pool's jobs that are currently running.
func (a *ActivityController) active() []*pool.Job {
	jobs := a.pool.Jobs()
	slices.SortFunc(jobs, func(x, y *pool.Job) int {
		return cmp.Compare(x.Media.Name, y.Media.Name)
	})
	return jobs
}
