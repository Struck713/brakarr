package controllers

import (
	"cmp"
	"context"
	"net/http"
	"slices"

	"github.com/Struck713/brakarr/internal/media"
	"github.com/Struck713/brakarr/internal/pool"
	"github.com/Struck713/brakarr/internal/web/templates"
	"github.com/labstack/echo/v5"
)

// HistoryStore is the persistence the activity history tab reads from.
type HistoryStore interface {
	ListHistory(ctx context.Context, limit, offset int) ([]media.History, error)
	CountHistory(ctx context.Context) (int, error)
}

// ActivityController serves the activity pages: active transcode jobs and the job history.
type ActivityController struct {
	db   HistoryStore
	pool *pool.Pool
}

// NewActivityController creates an ActivityController backed by the store and pool.
func NewActivityController(db HistoryStore, pool *pool.Pool) *ActivityController {
	return &ActivityController{db: db, pool: pool}
}

// Register adds the activity routes to the router.
func (a *ActivityController) Register(e *echo.Echo) {
	e.GET("/activity", a.Index)
	e.GET("/activity/history", a.History)
	e.GET("/jobs", a.Jobs)
	e.GET("/history", a.HistoryPanel)
}

// Index renders the full activity page.
func (a *ActivityController) Index(c *echo.Context) error {
	return Render(c, http.StatusOK, templates.ActivityPage(a.active()))
}

// Jobs renders the list of active jobs as a partial.
func (a *ActivityController) Jobs(c *echo.Context) error {
	return Render(c, http.StatusOK, templates.ActiveJobs(a.active()))
}

// History renders the full job history page.
func (a *ActivityController) History(c *echo.Context) error {
	list, err := a.history(c, pageParam(c))
	if err != nil {
		return err
	}
	return Render(c, http.StatusOK, templates.HistoryPage(list))
}

// HistoryPanel renders the job history panel as a partial.
func (a *ActivityController) HistoryPanel(c *echo.Context) error {
	list, err := a.history(c, pageParam(c))
	if err != nil {
		return err
	}
	return Render(c, http.StatusOK, templates.HistoryPanel(list))
}

// history loads one page of job history for display.
func (a *ActivityController) history(c *echo.Context, page int) (templates.HistoryList, error) {
	ctx := c.Request().Context()

	total, err := a.db.CountHistory(ctx)
	if err != nil {
		return templates.HistoryList{}, err
	}
	page, pages := pageBounds(page, total)

	items, err := a.db.ListHistory(ctx, pageSize, (page-1)*pageSize)
	if err != nil {
		return templates.HistoryList{}, err
	}
	return templates.HistoryList{Items: items, Page: page, Pages: pages, Total: total}, nil
}

// active returns the pool's jobs that are currently running.
func (a *ActivityController) active() []*pool.Job {
	jobs := a.pool.Jobs()
	slices.SortFunc(jobs, func(x, y *pool.Job) int {
		return cmp.Compare(x.Media.Name, y.Media.Name)
	})
	return jobs
}
