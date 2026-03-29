package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
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

func TestGenerateAutomationToken(t *testing.T) {
	tokenA, err := generateAutomationToken()
	if err != nil {
		t.Fatalf("generateAutomationToken returned error: %v", err)
	}
	tokenB, err := generateAutomationToken()
	if err != nil {
		t.Fatalf("generateAutomationToken returned error: %v", err)
	}

	if tokenA == "" || tokenB == "" {
		t.Fatal("expected non-empty automation tokens")
	}
	if tokenA == tokenB {
		t.Fatal("expected unique automation tokens")
	}
}

func TestBuildAutomationConnectURL(t *testing.T) {
	got := buildAutomationConnectURL(45678)
	want := "ws://127.0.0.1:45678/session"
	if got != want {
		t.Fatalf("buildAutomationConnectURL = %q, want %q", got, want)
	}
}

func TestExtractBidiConnectURL(t *testing.T) {
	line := "WebDriver BiDi listening on ws://127.0.0.1:46249"
	got, ok := extractBidiConnectURL(line)
	if !ok {
		t.Fatal("expected to extract a BiDi URL")
	}
	if got != "ws://127.0.0.1:46249/session" {
		t.Fatalf("extractBidiConnectURL = %q", got)
	}
}

func TestHandleAutomationInfoRequiresBearerToken(t *testing.T) {
	app := &App{
		automationConfig: AutomationConfig{
			Enabled:       true,
			APIListenAddr: "127.0.0.1:9090",
			APIToken:      "secret-token",
		},
		automationSessions: map[string]*AutomationSession{},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/automation/info", nil)
	rec := httptest.NewRecorder()

	app.handleAutomationInfo(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestHandleAutomationInfoReturnsMetadata(t *testing.T) {
	app := &App{
		automationConfig: AutomationConfig{
			Enabled:       true,
			APIListenAddr: "127.0.0.1:9090",
			APIToken:      "secret-token",
		},
		automationListenAddr: "127.0.0.1:9090",
		automationSessions: map[string]*AutomationSession{
			"one": {SessionID: "one"},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/automation/info", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rec := httptest.NewRecorder()

	app.handleAutomationInfo(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var payload automationResponse
	if err := json.NewDecoder(strings.NewReader(rec.Body.String())).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !payload.Success {
		t.Fatalf("expected success response, got %+v", payload)
	}

	dataBytes, err := json.Marshal(payload.Data)
	if err != nil {
		t.Fatalf("marshal nested data: %v", err)
	}

	var info AutomationInfo
	if err := json.Unmarshal(dataBytes, &info); err != nil {
		t.Fatalf("unmarshal info: %v", err)
	}
	if info.BaseURL != "http://127.0.0.1:9090" {
		t.Fatalf("base_url = %q", info.BaseURL)
	}
	if info.SessionCount != 1 {
		t.Fatalf("session_count = %d, want 1", info.SessionCount)
	}
}

func TestStartAutomationSessionReturnsExistingSessionForSameProfile(t *testing.T) {
	app := &App{
		profiles: []BrowserProfile{
			{ID: "profile-1", Name: "Profile A"},
		},
		automationConfig: AutomationConfig{
			Enabled:       true,
			APIListenAddr: "127.0.0.1:9090",
			APIToken:      "secret-token",
		},
		automationSessions: map[string]*AutomationSession{
			"session-1": {
				SessionID:   "session-1",
				ProfileID:   "profile-1",
				ProfileName: "Profile A",
				Status:      "running",
				DebugPort:   45678,
				ConnectURL:  "ws://127.0.0.1:45678/session",
				Protocol:    "bidi",
			},
		},
		automationRuntimes: map[string]*automationSessionRuntime{},
	}

	session, err := app.StartAutomationSession("profile-1", "")
	if err != nil {
		t.Fatalf("StartAutomationSession returned error: %v", err)
	}

	if session.SessionID != "session-1" {
		t.Fatalf("expected existing session to be reused, got %q", session.SessionID)
	}
	if len(app.automationSessions) != 1 {
		t.Fatalf("expected only one automation session, got %d", len(app.automationSessions))
	}
}

func TestSendBiDiCommandIgnoresAsyncMessages(t *testing.T) {
	server, wsURL := newBiDiTestServer(t, func(conn *websocket.Conn) {
		defer conn.Close()

		var command bidiCommandRequest
		if err := conn.ReadJSON(&command); err != nil {
			t.Errorf("read command: %v", err)
			return
		}

		if err := conn.WriteJSON(map[string]interface{}{
			"method": "log.entryAdded",
			"params": map[string]interface{}{"text": "noise"},
		}); err != nil {
			t.Errorf("write async event: %v", err)
			return
		}
		if err := conn.WriteJSON(map[string]interface{}{
			"id":     command.ID + 99,
			"result": map[string]interface{}{"ignored": true},
		}); err != nil {
			t.Errorf("write mismatched response: %v", err)
			return
		}
		if err := conn.WriteJSON(map[string]interface{}{
			"id":     command.ID,
			"result": map[string]interface{}{"ok": true},
		}); err != nil {
			t.Errorf("write matching response: %v", err)
		}
	})
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()

	result, err := sendBiDiCommand(conn, 1, "test.command", map[string]interface{}{"hello": "world"}, 2*time.Second)
	if err != nil {
		t.Fatalf("sendBiDiCommand returned error: %v", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if payload["ok"] != true {
		t.Fatalf("unexpected result payload: %+v", payload)
	}
}

func TestSendBiDiCommandTimesOut(t *testing.T) {
	server, wsURL := newBiDiTestServer(t, func(conn *websocket.Conn) {
		defer conn.Close()
		var command bidiCommandRequest
		if err := conn.ReadJSON(&command); err != nil {
			t.Errorf("read command: %v", err)
			return
		}
		time.Sleep(250 * time.Millisecond)
	})
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()

	_, err = sendBiDiCommand(conn, 1, "test.command", nil, 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStartAutomationSessionReusesExistingSessionAndNavigatesRequestedURL(t *testing.T) {
	navigateCalls := make(chan map[string]interface{}, 1)
	server, wsURL := newBiDiTestServer(t, func(conn *websocket.Conn) {
		defer conn.Close()

		for {
			var command bidiCommandRequest
			if err := conn.ReadJSON(&command); err != nil {
				return
			}

			switch command.Method {
			case "session.new":
				_ = conn.WriteJSON(map[string]interface{}{
					"id": command.ID,
					"error": map[string]interface{}{
						"error":   "session already active",
						"message": "session already active",
					},
				})
			case "browsingContext.getTree":
				_ = conn.WriteJSON(map[string]interface{}{
					"method": "network.beforeRequestSent",
					"params": map[string]interface{}{"context": "root-1"},
				})
				_ = conn.WriteJSON(map[string]interface{}{
					"id": command.ID,
					"result": map[string]interface{}{
						"contexts": []map[string]interface{}{
							{"context": "root-1"},
						},
					},
				})
			case "browsingContext.navigate":
				navigateCalls <- command.Params.(map[string]interface{})
				_ = conn.WriteJSON(map[string]interface{}{
					"id":     command.ID,
					"result": map[string]interface{}{"navigation": "nav-1"},
				})
				return
			}
		}
	})
	defer server.Close()

	app := &App{
		profiles: []BrowserProfile{
			{ID: "profile-1", Name: "Profile A", StartURL: "https://default.example"},
		},
		automationConfig: AutomationConfig{
			Enabled: true,
		},
		automationSessions: map[string]*AutomationSession{
			"session-1": {
				SessionID:   "session-1",
				ProfileID:   "profile-1",
				ProfileName: "Profile A",
				Status:      "running",
				DebugPort:   45678,
				ConnectURL:  wsURL,
				Protocol:    "bidi",
			},
		},
		automationRuntimes: map[string]*automationSessionRuntime{},
	}

	session, err := app.StartAutomationSession("profile-1", "chatgpt.com")
	if err != nil {
		t.Fatalf("StartAutomationSession returned error: %v", err)
	}
	if session.SessionID != "session-1" {
		t.Fatalf("expected reused session, got %q", session.SessionID)
	}
	if len(app.automationSessions) != 1 {
		t.Fatalf("expected only one automation session, got %d", len(app.automationSessions))
	}

	select {
	case navigate := <-navigateCalls:
		if got := navigate["url"]; got != "https://chatgpt.com" {
			t.Fatalf("navigate url = %v, want https://chatgpt.com", got)
		}
		if got := navigate["wait"]; got != "none" {
			t.Fatalf("navigate wait = %v, want none", got)
		}
		if got := navigate["context"]; got != "root-1" {
			t.Fatalf("navigate context = %v, want root-1", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected navigate call")
	}
}

func TestStartAutomationSessionUsesProfileDefaultURLForExistingSession(t *testing.T) {
	navigateCalls := make(chan map[string]interface{}, 1)
	server, wsURL := newBiDiTestServer(t, func(conn *websocket.Conn) {
		defer conn.Close()

		for {
			var command bidiCommandRequest
			if err := conn.ReadJSON(&command); err != nil {
				return
			}

			switch command.Method {
			case "session.new":
				_ = conn.WriteJSON(map[string]interface{}{
					"id":     command.ID,
					"result": map[string]interface{}{},
				})
			case "browsingContext.getTree":
				_ = conn.WriteJSON(map[string]interface{}{
					"id": command.ID,
					"result": map[string]interface{}{
						"contexts": []map[string]interface{}{
							{"context": "root-1"},
						},
					},
				})
			case "browsingContext.navigate":
				navigateCalls <- command.Params.(map[string]interface{})
				_ = conn.WriteJSON(map[string]interface{}{
					"id":     command.ID,
					"result": map[string]interface{}{"navigation": "nav-1"},
				})
				return
			}
		}
	})
	defer server.Close()

	app := &App{
		profiles: []BrowserProfile{
			{ID: "profile-1", Name: "Profile A", StartURL: "google.com"},
		},
		automationConfig: AutomationConfig{
			Enabled: true,
		},
		automationSessions: map[string]*AutomationSession{
			"session-1": {
				SessionID:   "session-1",
				ProfileID:   "profile-1",
				ProfileName: "Profile A",
				Status:      "running",
				DebugPort:   45678,
				ConnectURL:  wsURL,
				Protocol:    "bidi",
			},
		},
		automationRuntimes: map[string]*automationSessionRuntime{},
	}

	if _, err := app.StartAutomationSession("profile-1", ""); err != nil {
		t.Fatalf("StartAutomationSession returned error: %v", err)
	}

	select {
	case navigate := <-navigateCalls:
		if got := navigate["url"]; got != "https://google.com" {
			t.Fatalf("navigate url = %v, want https://google.com", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected navigate call")
	}
}

func TestSetAutomationEnabledRequiresNoActiveSessionsWhenDisabling(t *testing.T) {
	app := &App{
		dataDir: t.TempDir(),
		automationConfig: AutomationConfig{
			Enabled:       true,
			APIListenAddr: "127.0.0.1:9090",
			APIToken:      "secret-token",
		},
		automationSessions: map[string]*AutomationSession{
			"session-1": {
				SessionID: "session-1",
				ProfileID: "profile-1",
				Status:    "running",
			},
		},
		automationRuntimes: map[string]*automationSessionRuntime{},
	}

	err := app.SetAutomationEnabled(false)
	if err == nil {
		t.Fatal("expected disabling automation to fail when sessions are active")
	}
	if !app.automationConfig.Enabled {
		t.Fatal("expected automation to remain enabled after failure")
	}
}

func newBiDiTestServer(t *testing.T, handler func(*websocket.Conn)) (*httptest.Server, string) {
	t.Helper()

	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/session" {
			http.NotFound(w, r)
			return
		}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		handler(conn)
	}))

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/session"
	return server, wsURL
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
