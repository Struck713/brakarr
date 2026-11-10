package controllers

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Struck713/brakarr/internal/arr"
	"github.com/Struck713/brakarr/internal/config"
	"github.com/Struck713/brakarr/internal/web/templates"
	"github.com/labstack/echo/v5"
)

// SettingsController serves and persists the application settings.
type SettingsController struct {
	settings *config.Store
}

func NewSettingsController(settings *config.Store) *SettingsController {
	return &SettingsController{settings: settings}
}

func (s *SettingsController) Register(e *echo.Echo) {
	e.GET("/settings", s.Show)
	e.POST("/settings", s.Save)
}

func (s *SettingsController) Show(c *echo.Context) error {
	return Render(c, http.StatusOK, templates.SettingsPage(templates.SettingsView{
		Config: s.settings.Get(), Path: s.settings.Path(),
	}))
}

func (s *SettingsController) Save(c *echo.Context) error {
	cfg, err := s.fromForm(c)
	view := templates.SettingsView{Config: cfg, Path: s.settings.Path()}
	if err == nil {
		err = s.settings.Save(cfg)
	}
	status := http.StatusOK
	if err != nil {
		status = http.StatusUnprocessableEntity
		view.Errors = splitErrors(err)
	} else {
		view.Config, view.Saved = s.settings.Get(), true
	}
	return Render(c, status, templates.SettingsPage(view))
}

// settingsFromForm overlays the submitted form on the saved settings. The
// returned config is always usable to redisplay the form.
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
	cfg.VaapiDevice = text("vaapiDevice")
	integer("quality", "quality", &cfg.Quality)
	integer("workers", "workers", &cfg.Workers)
	cfg.WorkDir = text("workDir")
	cfg.ListenAddr = text("listenAddr")
	cfg.LogLevel = text("logLevel")
	cfg.DBPath = text("dbPath")

	cfg.Sonarr.URL = text("sonarrUrl")
	cfg.Radarr.URL = text("radarrUrl")
	// Keys are never sent to the browser, so a blank field keeps the saved one.
	if k := text("sonarrApiKey"); k != "" {
		cfg.Sonarr.APIKey = k
	}
	if k := text("radarrApiKey"); k != "" {
		cfg.Radarr.APIKey = k
	}
	if cfg.Sonarr.URL == "" {
		cfg.Sonarr.APIKey = ""
	}
	if cfg.Radarr.URL == "" {
		cfg.Radarr.APIKey = ""
	}

	cfg.ArrPathMap = nil
	for _, line := range strings.Split(c.FormValue("arrPathMap"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		local, remote, ok := strings.Cut(line, "=")
		if !ok {
			errs = append(errs, fmt.Errorf("path mapping %q must look like brakarr-path=app-path", strings.TrimSpace(line)))
			continue
		}
		cfg.ArrPathMap = append(cfg.ArrPathMap, arr.PathMap{Local: local, Remote: remote})
	}

	cfg.Normalize()
	return cfg, errors.Join(errs...)
}

// splitErrors flattens a joined error into one message per line.
func splitErrors(err error) []string {
	var out []string
	for _, line := range strings.Split(err.Error(), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}
