package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestMigrateVersion checks migrate's version handling:
//   - a file without "version" is treated as v0 and upgraded to currentVersion,
//   - a current file is left at currentVersion and reports it as its starting version,
//   - a version newer than this build is refused (so an older build never rewrites a newer file),
//   - a non-integer or negative version is refused.
func TestMigrateVersion(t *testing.T) {
	m := map[string]any{}
	if from, err := migrate(m); err != nil || from != 0 || m["version"] != currentVersion {
		t.Errorf("no version: from=%d err=%v version=%v", from, err, m["version"])
	}
	m = map[string]any{"version": float64(currentVersion)}
	if from, err := migrate(m); err != nil || from != currentVersion {
		t.Errorf("current: from=%d err=%v", from, err)
	}
	for _, v := range []any{float64(currentVersion + 1), 1.5, float64(-1), "2"} {
		if _, err := migrate(map[string]any{"version": v}); err == nil {
			t.Errorf("version %v accepted; want error", v)
		}
	}
}

// TestMigrateServices checks the v0 -> v1 step that turns the old single
// sonarr/radarr objects into the services list:
//   - each old entry with a URL becomes an enabled service with a fixed ID, name and type,
//   - an entry with a blank URL is dropped,
//   - the old keys are always removed,
//   - if the file already has services, the old entries are dropped instead of merged,
//   - a non-object sonarr/radarr value is an error.
func TestMigrateServices(t *testing.T) {
	m := map[string]any{
		"sonarr": map[string]any{"url": "http://sonarr:8989", "apiKey": "s"},
		"radarr": map[string]any{"url": "  ", "apiKey": "r"},
	}
	if err := migrateServices(m); err != nil {
		t.Fatal(err)
	}
	want := []any{map[string]any{
		"id": "sonarr", "name": "Sonarr", "type": "sonarr",
		"url": "http://sonarr:8989", "apiKey": "s", "enabled": true,
	}}
	if !reflect.DeepEqual(m["services"], want) {
		t.Errorf("services = %v\nwant %v", m["services"], want)
	}
	if _, ok := m["sonarr"]; ok {
		t.Error("sonarr key not removed")
	}
	if _, ok := m["radarr"]; ok {
		t.Error("radarr key not removed")
	}

	existing := []any{map[string]any{"id": "x"}}
	m = map[string]any{"services": existing, "sonarr": map[string]any{"url": "http://s"}}
	if err := migrateServices(m); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(m["services"], existing) {
		t.Errorf("existing services changed: %v", m["services"])
	}

	if err := migrateServices(map[string]any{"sonarr": "http://s"}); err == nil {
		t.Error("string sonarr value accepted; want error")
	}
}

// TestMigrateDevice checks the v1 -> v2 step that renames vaapiDevice to device:
//   - with no encoder set (files of this age predate it), the value moves to device,
//   - an already-set device is not overwritten,
//   - for a non-VAAPI encoder the VAAPI path is dropped instead of becoming its device,
//   - vaapiDevice is always removed, and a non-string value is an error.
func TestMigrateDevice(t *testing.T) {
	tests := []struct {
		name string
		in   map[string]any
		want map[string]any
	}{
		{"moves", map[string]any{"vaapiDevice": "/dev/dri/renderD129"},
			map[string]any{"device": "/dev/dri/renderD129"}},
		{"keeps device", map[string]any{"vaapiDevice": "/a", "device": "/b"},
			map[string]any{"device": "/b"}},
		{"nvenc", map[string]any{"vaapiDevice": "/a", "encoder": "nvenc"},
			map[string]any{"encoder": "nvenc"}},
		{"absent", map[string]any{}, map[string]any{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := migrateDevice(tt.in); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(tt.in, tt.want) {
				t.Errorf("got %v, want %v", tt.in, tt.want)
			}
		})
	}
	if err := migrateDevice(map[string]any{"vaapiDevice": 1.0}); err == nil {
		t.Error("numeric vaapiDevice accepted; want error")
	}
}

// TestReadMigratesOldFile is an end-to-end check that a real v0 config file on
// disk loads into the current Config: migrations run, missing fields take
// their defaults, values are normalized, and read reports that the file should
// be rewritten.
func TestReadMigratesOldFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	old := `{
		"crawlPath": "/media",
		"extensions": ["MKV"],
		"vaapiDevice": "/dev/dri/renderD129",
		"sonarr": {"url": "http://sonarr:8989", "apiKey": "key"}
	}`
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	c, rewrite, err := read(path)
	if err != nil {
		t.Fatal(err)
	}
	if !rewrite {
		t.Error("old file not flagged for rewrite")
	}
	if c.CrawlPath != "/media" || c.Device != "/dev/dri/renderD129" || c.Quality != Defaults().Quality {
		t.Errorf("config = %+v", c)
	}
	if !reflect.DeepEqual(c.Extensions, []string{".mkv"}) {
		t.Errorf("extensions = %v; want normalized [.mkv]", c.Extensions)
	}
	if len(c.Services) != 1 || c.Services[0].ID != "sonarr" || !c.Services[0].Enabled {
		t.Errorf("services = %+v", c.Services)
	}
}

// TestReadErrors checks read's failure modes: a missing file yields defaults
// and asks to be written; invalid JSON and unknown fields (e.g. a typo'd key)
// are errors that name the file.
func TestReadErrors(t *testing.T) {
	dir := t.TempDir()
	c, rewrite, err := read(filepath.Join(dir, "missing.json"))
	if err != nil || !rewrite || !reflect.DeepEqual(c, Defaults()) {
		t.Errorf("missing file: rewrite=%v err=%v", rewrite, err)
	}
	for name, body := range map[string]string{
		"bad.json":     `{`,
		"unknown.json": `{"version": 2, "crawlPth": "/media"}`,
	} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := read(path); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: err = %v; want error naming the file", name, err)
		}
	}
}
