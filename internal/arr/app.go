package arr

import "strings"

// App identifies one configured Sonarr or Radarr instance and its API.
type App struct {
	Name, BaseURL, APIKey      string
	ListPath, Command, IDField string
}

// Type describes a supported kind of service and how to talk to its API.
type Type struct {
	ID    string // stored in the config, such as "sonarr"
	Label string // shown in the UI, such as "Sonarr"

	listPath, command, idField string
}

// types lists the supported service types. Adding a service is one entry here.
var types = []Type{
	{"sonarr", "Sonarr", "/api/v3/series", "RescanSeries", "seriesId"},
	{"radarr", "Radarr", "/api/v3/movie", "RescanMovie", "movieId"},
}

// Types returns the supported service types in display order.
func Types() []Type { return append([]Type(nil), types...) }

// Known reports whether id is a supported service type.
func Known(id string) bool {
	for _, t := range types {
		if t.ID == id {
			return true
		}
	}
	return false
}

// New returns an App of the given type, or false if the type is unknown.
// The name identifies the instance in logs.
func New(typ, name, url, key string) (App, bool) {
	for _, t := range types {
		if t.ID == typ {
			return App{name, strings.TrimRight(url, "/"), key, t.listPath, t.command, t.idField}, true
		}
	}
	return App{}, false
}
