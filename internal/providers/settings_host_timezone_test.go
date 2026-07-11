package providers

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTimezoneFromLocaltimeLink(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Fake zoneinfo tree with a real IANA name so LoadLocation succeeds.
	zonePath := filepath.Join(dir, "zoneinfo", "Asia", "Tokyo")
	if err := os.MkdirAll(filepath.Dir(zonePath), 0o755); err != nil {
		t.Fatal(err)
	}
	// Content is unused; LoadLocation uses the name suffix only after we parse the path.
	if err := os.WriteFile(zonePath, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "localtime")
	if err := os.Symlink(zonePath, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	name, ok := timezoneFromLocaltimeLink(link)
	if !ok {
		t.Fatal("expected ok")
	}
	if name != "Asia/Tokyo" {
		t.Fatalf("name = %q, want Asia/Tokyo", name)
	}
	if _, err := time.LoadLocation(name); err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
}

func TestTimezoneFromLocaltimeLink_Missing(t *testing.T) {
	t.Parallel()
	if _, ok := timezoneFromLocaltimeLink(filepath.Join(t.TempDir(), "nope")); ok {
		t.Fatal("expected not ok for missing path")
	}
}

func TestHostTimezoneName_NeverLocal(t *testing.T) {
	// Not parallel: may depend on process TZ.
	name := hostTimezoneName()
	if name == "" {
		t.Fatal("empty")
	}
	if name == "Local" {
		t.Fatal(`must never return "Local"`)
	}
	if _, err := time.LoadLocation(name); err != nil {
		t.Fatalf("LoadLocation(%q): %v", name, err)
	}
}

func TestHostTimezoneName_PrefersTZ(t *testing.T) {
	// TZ is checked every call (not cached), so Setenv is observable.
	t.Setenv("TZ", "Europe/Berlin")
	name := hostTimezoneName()
	if name != "Europe/Berlin" {
		t.Fatalf("got %q, want Europe/Berlin", name)
	}
}

func TestHostTimezoneName_IgnoresInvalidTZ(t *testing.T) {
	t.Setenv("TZ", "Not/A_Real_Zone")
	name := hostTimezoneName()
	if name == "Not/A_Real_Zone" {
		t.Fatal("must not use invalid TZ")
	}
	if name == "Local" {
		t.Fatal(`must never return "Local"`)
	}
	if _, err := time.LoadLocation(name); err != nil {
		t.Fatalf("LoadLocation(%q): %v", name, err)
	}
}
