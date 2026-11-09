package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseValue(t *testing.T) {
	tests := map[string]string{
		`true`:                       "true",
		`true   # comment`:           "true",
		`  true  `:                   "true",
		`"a # b"`:                    "a # b",
		`'a # b' # trailing`:         "a # b",
		`"quoted"`:                   "quoted",
		`/path/with#hash`:            "/path/with#hash",
		`value#nospace`:              "value#nospace",
		`10m        # 0 = scan once`: "10m",
		``:                           "",
	}
	for in, want := range tests {
		if got := parseValue(in); got != want {
			t.Errorf("parseValue(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLoadDotEnvStripsInlineComments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("BRAKARR_TEST_KEY=abc   # note\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BRAKARR_TEST_KEY", "")
	os.Unsetenv("BRAKARR_TEST_KEY")
	defer os.Unsetenv("BRAKARR_TEST_KEY")
	if err := loadDotEnv(path); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("BRAKARR_TEST_KEY"); got != "abc" {
		t.Errorf("got %q, want abc", got)
	}
}

func env(t *testing.T, kv map[string]string) func(string) string {
	t.Helper()
	if _, ok := kv["CRAWL_PATH"]; !ok {
		kv["CRAWL_PATH"] = t.TempDir()
	}
	return func(k string) string { return kv[k] }
}

func TestInvalidBooleanIsAnError(t *testing.T) {
	_, err := fromEnv(env(t, map[string]string{"SKIP_HARDLINKED": "maybe"}))
	if err == nil || !strings.Contains(err.Error(), "SKIP_HARDLINKED") {
		t.Errorf("err = %v, want SKIP_HARDLINKED error", err)
	}
}

func TestSkipHardlinkedDefaultsToTrue(t *testing.T) {
	c, err := fromEnv(env(t, map[string]string{}))
	if err != nil {
		t.Fatal(err)
	}
	if !c.SkipHardlinked {
		t.Error("SkipHardlinked = false, want true")
	}
}

func TestDefaultExtensionsIncludeMKVAndMP4(t *testing.T) {
	c, err := fromEnv(env(t, map[string]string{}))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(c.Extensions, ","); got != ".mkv,.mp4" {
		t.Errorf("extensions = %q", got)
	}
}

func TestUnsupportedExtensionIsAnError(t *testing.T) {
	_, err := fromEnv(env(t, map[string]string{"CRAWL_EXTENSIONS": ".mkv,avi"}))
	if err == nil || !strings.Contains(err.Error(), ".avi") {
		t.Errorf("err = %v, want .avi error", err)
	}
}
