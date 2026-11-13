package controllers

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Struck713/brakarr/internal/config"
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

// SettingsController serves the settings page and persists changes.
type SettingsController struct {
	settings *config.Store
	runtime  Runtime
}

// NewSettingsController creates a SettingsController backed by the config store.
func NewSettingsController(settings *config.Store, runtime Runtime) *SettingsController {
	return &SettingsController{settings: settings, runtime: runtime}
}

// view builds the settings page view for cfg with the current runtime state.
func (s *SettingsController) view(cfg config.Config) templates.SettingsView {
	v := templates.SettingsView{Config: cfg, Path: s.settings.Path(), Restart: s.runtime.NeedsRestart()}
	if err := s.runtime.Disabled(); err != nil {
		v.Disabled = splitErrors(err)
	}
	return v
}

// Register adds the settings routes to the router.
func (s *SettingsController) Register(e *echo.Echo) {
	e.GET("/settings", s.Show)
	e.POST("/settings", s.Save)
}

// Show renders the settings page with the current configuration.
func (s *SettingsController) Show(c *echo.Context) error {
	return Render(c, http.StatusOK, templates.SettingsPage(s.view(s.settings.Get())))
}

// Save validates the submitted form and writes the new configuration.
func (s *SettingsController) Save(c *echo.Context) error {
	cfg, err := s.fromForm(c)
	if err == nil {
		err = s.settings.Save(cfg)
	}
	status := http.StatusOK
	view := s.view(cfg)
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
