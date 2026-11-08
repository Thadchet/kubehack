package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadOnlySurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "settings.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetReadOnly("prod", true); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reopened.IsReadOnly("prod") || reopened.IsReadOnly("dev") {
		t.Errorf("contexts = %v", reopened.ReadOnlyContexts())
	}

	if err := reopened.SetReadOnly("prod", false); err != nil {
		t.Fatal(err)
	}
	if reopened.IsReadOnly("prod") {
		t.Error("still read-only")
	}
}

func TestSettingTwiceKeepsOneEntry(t *testing.T) {
	store, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := store.SetReadOnly("prod", true); err != nil {
			t.Fatal(err)
		}
	}
	if got := store.ReadOnlyContexts(); len(got) != 1 {
		t.Errorf("contexts = %v", got)
	}
}

func TestCorruptFileIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Error("want error, because guessing could unprotect a context")
	}
}

func TestFailedSaveLeavesStateUnchanged(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(filepath.Join(blocker, "settings.json"))
	if err == nil {
		if err := store.SetReadOnly("prod", true); err == nil {
			t.Fatal("want save error")
		}
		if store.IsReadOnly("prod") {
			t.Error("state changed although the save failed")
		}
	}
}

func TestFileIsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetReadOnly("prod", true); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", info.Mode().Perm())
	}
}

func TestRecentForwardsSurviveReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	web := RecentForward{Context: "kind", Namespace: "default", Kind: "service", Name: "web"}
	http, metrics := web, web
	http.Port, http.LocalPort = 80, 8080
	metrics.Port, metrics.LocalPort = 9090, 19090
	for _, forward := range []RecentForward{http, metrics} {
		if err := store.RememberForward(forward); err != nil {
			t.Fatal(err)
		}
	}
	// Forwarding port 80 again moves it to the front with its new local port.
	http.LocalPort = 8081
	if err := store.RememberForward(http); err != nil {
		t.Fatal(err)
	}
	if err := store.SetReadOnly("prod", true); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got := reopened.RecentForwards("kind", "default", "service", "web")
	if len(got) != 2 || got[0] != http || got[1] != metrics {
		t.Errorf("forwards = %+v", got)
	}
	if other := reopened.RecentForwards("kind", "default", "pod", "web"); len(other) != 0 {
		t.Errorf("pod web = %+v", other)
	}
	if !reopened.IsReadOnly("prod") {
		t.Error("remembering a forward lost the read-only contexts")
	}
}
