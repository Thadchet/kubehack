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
