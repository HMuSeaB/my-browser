package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeStartURL(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "empty", input: "", want: ""},
		{name: "domain only", input: "google.com", want: "https://google.com"},
		{name: "https kept", input: "https://chatgpt.com", want: "https://chatgpt.com"},
		{name: "http kept", input: "http://localhost:3000", want: "http://localhost:3000"},
		{name: "invalid scheme", input: "ftp://example.com", wantErr: true},
		{name: "invalid url", input: "://bad", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeStartURL(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeStartURL returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("normalizeStartURL(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestUpdateProfileNormalizesStartURL(t *testing.T) {
	app := &App{
		profiles: []BrowserProfile{
			{ID: "profile-1", Name: "Test"},
		},
		dataDir: t.TempDir(),
	}

	err := app.UpdateProfile(BrowserProfile{
		ID:       "profile-1",
		Name:     "Test",
		StartURL: "chatgpt.com",
	})
	if err != nil {
		t.Fatalf("UpdateProfile returned error: %v", err)
	}

	if got := app.profiles[0].StartURL; got != "https://chatgpt.com" {
		t.Fatalf("StartURL = %q, want https://chatgpt.com", got)
	}
}

func TestInitializeStorageUsesLocalAppData(t *testing.T) {
	localAppData := t.TempDir()
	t.Setenv("LOCALAPPDATA", localAppData)

	app := &App{
		legacyDataDir: filepath.Join(t.TempDir(), "legacy-data"),
	}

	if err := app.initializeStorage(); err != nil {
		t.Fatalf("initializeStorage returned error: %v", err)
	}

	wantDir := filepath.Join(localAppData, "MyBrowser")
	if app.dataDir != wantDir {
		t.Fatalf("dataDir = %q, want %q", app.dataDir, wantDir)
	}

	if info, err := os.Stat(wantDir); err != nil || !info.IsDir() {
		t.Fatalf("expected storage directory to exist: %v", err)
	}
}

func TestInitializeStorageUsesPortableDirectoryWhenPortableFlagExists(t *testing.T) {
	portableBaseDir := t.TempDir()
	localAppData := t.TempDir()
	t.Setenv("LOCALAPPDATA", localAppData)

	if err := os.WriteFile(filepath.Join(portableBaseDir, "portable.flag"), []byte("1"), 0644); err != nil {
		t.Fatalf("write portable flag: %v", err)
	}

	app := &App{
		portableBaseDirs: []string{portableBaseDir},
		legacyDataDir:    filepath.Join(t.TempDir(), "legacy-data"),
	}

	if err := app.initializeStorage(); err != nil {
		t.Fatalf("initializeStorage returned error: %v", err)
	}

	wantDir := filepath.Join(portableBaseDir, "MyBrowserData")
	if app.dataDir != wantDir {
		t.Fatalf("portable dataDir = %q, want %q", app.dataDir, wantDir)
	}
	if app.GetStorageMode() != "portable" {
		t.Fatalf("storage mode = %q, want portable", app.GetStorageMode())
	}
}

func TestInitializeStorageMigratesLegacyDataWhenTargetIsEmpty(t *testing.T) {
	legacyDir := filepath.Join(t.TempDir(), "legacy-data")
	targetDir := filepath.Join(t.TempDir(), "MyBrowser")
	if err := os.MkdirAll(filepath.Join(legacyDir, "profiles", "alpha"), 0755); err != nil {
		t.Fatalf("create legacy profile dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "profiles.json"), []byte(`{"legacy":true}`), 0644); err != nil {
		t.Fatalf("write legacy profiles.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "proxies.json"), []byte(`[]`), 0644); err != nil {
		t.Fatalf("write legacy proxies.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "profiles", "alpha", "cookies.sqlite"), []byte("sqlite"), 0644); err != nil {
		t.Fatalf("write legacy profile data: %v", err)
	}
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		t.Fatalf("create empty target dir: %v", err)
	}

	app := &App{
		dataDir:       targetDir,
		legacyDataDir: legacyDir,
	}

	if err := app.initializeStorage(); err != nil {
		t.Fatalf("initializeStorage returned error: %v", err)
	}

	assertFileContent(t, filepath.Join(targetDir, "profiles.json"), `{"legacy":true}`)
	assertFileContent(t, filepath.Join(targetDir, "proxies.json"), `[]`)
	assertFileContent(t, filepath.Join(targetDir, "profiles", "alpha", "cookies.sqlite"), "sqlite")

	if app.storageMigrationNote == "" {
		t.Fatal("expected migration note to be recorded")
	}

	if _, err := os.Stat(filepath.Join(legacyDir, "profiles.json")); err != nil {
		t.Fatalf("expected legacy data to be preserved, got err: %v", err)
	}
}

func TestInitializeStorageSkipsMigrationWhenTargetAlreadyHasData(t *testing.T) {
	legacyDir := filepath.Join(t.TempDir(), "legacy-data")
	targetDir := filepath.Join(t.TempDir(), "MyBrowser")
	if err := os.MkdirAll(legacyDir, 0755); err != nil {
		t.Fatalf("create legacy dir: %v", err)
	}
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		t.Fatalf("create target dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "profiles.json"), []byte(`{"legacy":true}`), 0644); err != nil {
		t.Fatalf("write legacy profiles.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "profiles.json"), []byte(`{"current":true}`), 0644); err != nil {
		t.Fatalf("write target profiles.json: %v", err)
	}

	app := &App{
		dataDir:       targetDir,
		legacyDataDir: legacyDir,
	}

	if err := app.initializeStorage(); err != nil {
		t.Fatalf("initializeStorage returned error: %v", err)
	}

	assertFileContent(t, filepath.Join(targetDir, "profiles.json"), `{"current":true}`)
	if app.storageMigrationNote != "" {
		t.Fatalf("expected no migration note, got %q", app.storageMigrationNote)
	}
}

func TestInitializeStorageFallsBackToLaterLegacyDirectory(t *testing.T) {
	emptyLegacyDir := filepath.Join(t.TempDir(), "empty-legacy")
	actualLegacyDir := filepath.Join(t.TempDir(), "actual-legacy")
	targetDir := filepath.Join(t.TempDir(), "MyBrowser")

	if err := os.MkdirAll(emptyLegacyDir, 0755); err != nil {
		t.Fatalf("create empty legacy dir: %v", err)
	}
	if err := os.MkdirAll(actualLegacyDir, 0755); err != nil {
		t.Fatalf("create actual legacy dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(actualLegacyDir, "profiles.json"), []byte(`{"legacy":"workspace"}`), 0644); err != nil {
		t.Fatalf("write actual legacy profiles.json: %v", err)
	}

	app := &App{
		dataDir:        targetDir,
		legacyDataDirs: []string{emptyLegacyDir, actualLegacyDir},
	}

	if err := app.initializeStorage(); err != nil {
		t.Fatalf("initializeStorage returned error: %v", err)
	}

	assertFileContent(t, filepath.Join(targetDir, "profiles.json"), `{"legacy":"workspace"}`)
	if app.storageMigrationNote == "" {
		t.Fatal("expected migration note for fallback legacy directory")
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != want {
		t.Fatalf("content mismatch for %s: got %q want %q", path, string(got), want)
	}
}
