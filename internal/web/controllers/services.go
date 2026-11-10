package controllers

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Struck713/brakarr/internal/arr"
	"github.com/Struck713/brakarr/internal/config"
	"github.com/Struck713/brakarr/internal/web/templates"
	"github.com/labstack/echo/v5"
)

const servicesPath = "/settings/services"

var errServiceNotFound = errors.New("service not found")

// ServicesController manages Sonarr and Radarr instances and path mappings.
type ServicesController struct {
	settings *config.Store
}

// NewServicesController creates a ServicesController backed by the config store.
func NewServicesController(settings *config.Store) *ServicesController {
	return &ServicesController{settings: settings}
}

// Register adds the services routes to the router.
func (s *ServicesController) Register(e *echo.Echo) {
	e.GET(servicesPath, s.List)
	e.GET(servicesPath+"/new", s.New)
	e.POST(servicesPath, s.Create)
	e.GET(servicesPath+"/:id", s.Edit)
	e.POST(servicesPath+"/:id", s.Update)
	e.POST(servicesPath+"/:id/delete", s.Delete)
	e.POST(servicesPath+"/mappings", s.SaveMappings)
}

func (s *ServicesController) listView(c *echo.Context) templates.ServicesView {
	cfg := s.settings.Get()
	return templates.ServicesView{
		Services: cfg.Services, PathMap: cfg.ArrPathMap,
		Path: s.settings.Path(), Saved: c.QueryParam("saved") != "",
	}
}

// List shows the configured services and the path mappings.
func (s *ServicesController) List(c *echo.Context) error {
	return Render(c, http.StatusOK, templates.ServicesPage(s.listView(c)))
}

// New shows the empty add form.
func (s *ServicesController) New(c *echo.Context) error {
	return Render(c, http.StatusOK, templates.ServiceFormPage(templates.ServiceFormView{
		Service: config.Service{Type: arr.Types()[0].ID, Enabled: true}, IsNew: true,
	}))
}

// Edit shows the edit form for one service.
func (s *ServicesController) Edit(c *echo.Context) error {
	svc, ok := s.find(c.Param("id"))
	if !ok {
		return echo.ErrNotFound
	}
	return Render(c, http.StatusOK, templates.ServiceFormPage(templates.ServiceFormView{
		Service: svc, KeySet: svc.APIKey != "",
	}))
}

// Create validates the form and adds a new service.
func (s *ServicesController) Create(c *echo.Context) error {
	svc := serviceFromForm(c)
	err := checkURL(svc.URL)
	if err == nil {
		err = s.settings.UpdateServices(func(cfg *config.Config) error {
			cfg.Services = append(cfg.Services, svc)
			return nil
		})
	}
	if err != nil {
		svc.APIKey = ""
		return Render(c, http.StatusUnprocessableEntity, templates.ServiceFormPage(templates.ServiceFormView{
			Service: svc, IsNew: true, Errors: splitErrors(err),
		}))
	}
	return c.Redirect(http.StatusSeeOther, servicesPath+"?saved=1")
}

// Update validates the form and changes an existing service.
func (s *ServicesController) Update(c *echo.Context) error {
	id := c.Param("id")
	svc := serviceFromForm(c)
	svc.ID = id
	keySet := false
	err := checkURL(svc.URL)
	if err == nil {
		err = s.settings.UpdateServices(func(cfg *config.Config) error {
			for i := range cfg.Services {
				if cfg.Services[i].ID != id {
					continue
				}
				// Keys are never sent to the browser, so a blank field keeps the saved one.
				if svc.APIKey == "" {
					svc.APIKey = cfg.Services[i].APIKey
				}
				cfg.Services[i] = svc
				return nil
			}
			return errServiceNotFound
		})
	}
	if errors.Is(err, errServiceNotFound) {
		return echo.ErrNotFound
	}
	if err != nil {
		if old, ok := s.find(id); ok {
			keySet = old.APIKey != ""
		} else {
			return echo.ErrNotFound
		}
		svc.APIKey = ""
		return Render(c, http.StatusUnprocessableEntity, templates.ServiceFormPage(templates.ServiceFormView{
			Service: svc, KeySet: keySet, Errors: splitErrors(err),
		}))
	}
	return c.Redirect(http.StatusSeeOther, servicesPath+"?saved=1")
}

// Delete removes a service.
func (s *ServicesController) Delete(c *echo.Context) error {
	id := c.Param("id")
	err := s.settings.UpdateServices(func(cfg *config.Config) error {
		for i := range cfg.Services {
			if cfg.Services[i].ID == id {
				cfg.Services = append(cfg.Services[:i], cfg.Services[i+1:]...)
				return nil
			}
		}
		return errServiceNotFound
	})
	if errors.Is(err, errServiceNotFound) {
		return echo.ErrNotFound
	}
	if err != nil {
		view := s.listView(c)
		view.Errors = splitErrors(err)
		return Render(c, http.StatusUnprocessableEntity, templates.ServicesPage(view))
	}
	return c.Redirect(http.StatusSeeOther, servicesPath+"?saved=1")
}

// SaveMappings parses and stores the path mappings.
func (s *ServicesController) SaveMappings(c *echo.Context) error {
	text := c.FormValue("arrPathMap")
	maps, err := parsePathMap(text)
	if err == nil {
		err = s.settings.UpdateServices(func(cfg *config.Config) error {
			cfg.ArrPathMap = maps
			return nil
		})
	}
	if err != nil {
		view := s.listView(c)
		view.Saved, view.Errors, view.MapText, view.MapTextSet = false, splitErrors(err), text, true
		return Render(c, http.StatusUnprocessableEntity, templates.ServicesPage(view))
	}
	return c.Redirect(http.StatusSeeOther, servicesPath+"?saved=1")
}

func (s *ServicesController) find(id string) (config.Service, bool) {
	for _, svc := range s.settings.Get().Services {
		if svc.ID == id {
			return svc, true
		}
	}
	return config.Service{}, false
}

// serviceFromForm reads the submitted service fields.
func serviceFromForm(c *echo.Context) config.Service {
	text := func(name string) string { return strings.TrimSpace(c.FormValue(name)) }
	return config.Service{
		Name: text("name"), Type: text("type"), URL: text("url"),
		APIKey: text("apiKey"), Enabled: c.FormValue("enabled") != "",
	}
}

// checkURL requires an http or https URL. It lives here rather than in
// Config.Validate so a malformed URL in an existing file cannot disable
// transcoding at startup. A blank URL is left to the config validation.
func checkURL(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("URL must be a valid http:// or https:// address")
	}
	return nil
}

// parsePathMap parses one brakarr-path=app-path mapping per line.
func parsePathMap(text string) ([]arr.PathMap, error) {
	var maps []arr.PathMap
	var errs []error
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		local, remote, ok := strings.Cut(line, "=")
		if !ok {
			errs = append(errs, fmt.Errorf("path mapping %q must look like brakarr-path=app-path", strings.TrimSpace(line)))
			continue
		}
		maps = append(maps, arr.PathMap{Local: local, Remote: remote})
	}
	return maps, errors.Join(errs...)
}
