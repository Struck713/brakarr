package controllers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Struck713/brakarr/internal/config"
	"github.com/Struck713/brakarr/internal/pool"
	"github.com/Struck713/brakarr/internal/web/templates"
	"github.com/labstack/echo/v5"
)

// Runtime reports the state of the components that saved settings are applied to.
type Runtime interface {
	// Disabled returns why transcoding is not running, or nil if it is.
	Disabled() error
	// NeedsRestart lists the saved settings that only take effect after a restart.
	NeedsRestart() []string
}

// SettingsStore is the persistence the settings page uses to requeue files skipped for low
// quality that pass the current minimum VMAF.
type SettingsStore interface {
	CountPassingQualitySkips(ctx context.Context, minVMAF float64) (int, error)
	RequeuePassingQualitySkips(ctx context.Context, minVMAF float64) (int, error)
}

// SettingsController serves the settings page and persists changes.
type SettingsController struct {
	settings *config.Store
	runtime  Runtime
	db       SettingsStore
	pool     *pool.Pool
}

// NewSettingsController creates a SettingsController backed by the config store.
func NewSettingsController(settings *config.Store, runtime Runtime, db SettingsStore, pool *pool.Pool) *SettingsController {
	return &SettingsController{settings: settings, runtime: runtime, db: db, pool: pool}
}

// view builds the settings page view for cfg with the current runtime state.
func (s *SettingsController) view(ctx context.Context, cfg config.Config) (templates.SettingsView, error) {
	saved := s.settings.Get()
	v := templates.SettingsView{Config: cfg, Path: s.settings.Path(), Restart: s.runtime.NeedsRestart(),
		MinVMAF: saved.MinVMAF}
	if err := s.runtime.Disabled(); err != nil {
		v.Disabled = splitErrors(err)
	}
	n, err := s.db.CountPassingQualitySkips(ctx, float64(saved.MinVMAF))
	if err != nil {
		return v, err
	}
	v.PassingSkips = n
	return v, nil
}

// Register adds the settings routes to the router.
func (s *SettingsController) Register(e *echo.Echo) {
	e.GET("/settings", s.Show)
	e.POST("/settings", s.Save)
	e.POST("/settings/requeue-quality", s.RequeueQuality)
}

// Show renders the settings page with the current configuration.
func (s *SettingsController) Show(c *echo.Context) error {
	view, err := s.view(c.Request().Context(), s.settings.Get())
	if err != nil {
		return err
	}
	return Render(c, http.StatusOK, templates.SettingsPage(view))
}

// RequeueQuality requeues the files skipped for low quality that pass the current minimum VMAF.
func (s *SettingsController) RequeueQuality(c *echo.Context) error {
	ctx := c.Request().Context()
	n, err := s.db.RequeuePassingQualitySkips(ctx, float64(s.settings.Get().MinVMAF))
	if err != nil {
		return err
	}
	if n > 0 {
		s.pool.Notify()
	}
	view, err := s.view(ctx, s.settings.Get())
	if err != nil {
		return err
	}
	view.Requeued = n
	return Render(c, http.StatusOK, templates.SettingsPage(view))
}

// Save validates the submitted form and writes the new configuration.
func (s *SettingsController) Save(c *echo.Context) error {
	cfg, err := s.fromForm(c)
	if err == nil {
		err = s.settings.Save(cfg)
	}
	status := http.StatusOK
	view, viewErr := s.view(c.Request().Context(), cfg)
	if viewErr != nil {
		return viewErr
	}
	if err != nil {
		status = http.StatusUnprocessableEntity
		view.Errors = splitErrors(err)
	} else {
		view.Config, view.Saved = s.settings.Get(), true
	}
	return Render(c, status, templates.SettingsPage(view))
}

// fromForm builds a Config from the submitted form values.
func (s *SettingsController) fromForm(c *echo.Context) (config.Config, error) {
	cfg := s.settings.Get()
	var errs []error
	text := func(name string) string { return strings.TrimSpace(c.FormValue(name)) }
	integer := func(name, label string, dst *int) {
		n, err := strconv.Atoi(text(name))
		if err != nil {
			errs = append(errs, fmt.Errorf("%s must be a whole number", label))
			return
		}
		*dst = n
	}
	duration := func(name, label string, dst *config.Duration) {
		if err := dst.UnmarshalText([]byte(text(name))); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", label, err))
		}
	}

	cfg.CrawlPath = text("crawlPath")
	cfg.Extensions = strings.Split(text("extensions"), ",")
	duration("crawlInterval", "scan interval", &cfg.CrawlInterval)
	duration("minFileAge", "minimum file age", &cfg.MinFileAge)
	cfg.SkipHardlinked = c.FormValue("skipHardlinked") != ""
	cfg.Encoder = text("encoder")
	cfg.Device = text("device")
	integer("quality", "quality", &cfg.Quality)
	integer("minVmaf", "minimum VMAF", &cfg.MinVMAF)
	integer("vmafSamples", "VMAF samples", &cfg.VMAFSamples)
	integer("workers", "workers", &cfg.Workers)
	cfg.WorkDir = text("workDir")
	cfg.ListenAddr = text("listenAddr")
	cfg.LogLevel = text("logLevel")
	cfg.DBPath = text("dbPath")

	cfg.Normalize()
	return cfg, errors.Join(errs...)
}
