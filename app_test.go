package main

import (
	"archive/zip"
	"database/sql"
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

func TestNormalizeCategory(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty", input: "", want: ""},
		{name: "trim spaces", input: "  工作  ", want: "工作"},
		{name: "collapse whitespace", input: "AI   Tools", want: "AI Tools"},
		{name: "preserve chinese words", input: "  电商   账号  ", want: "电商 账号"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeCategory(tt.input); got != tt.want {
				t.Fatalf("normalizeCategory(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestCreateProfileNormalizesCategory(t *testing.T) {
	app := &App{
		profiles: []BrowserProfile{},
		dataDir:  t.TempDir(),
	}

	profile, err := app.CreateProfile("分类环境", "", "", "chatgpt.com", "  AI   Tools ")
	if err != nil {
		t.Fatalf("CreateProfile returned error: %v", err)
	}

	if profile.Category != "AI Tools" {
		t.Fatalf("Category = %q, want %q", profile.Category, "AI Tools")
	}
	if got := app.profiles[0].Category; got != "AI Tools" {
		t.Fatalf("saved category = %q, want %q", got, "AI Tools")
	}
}

func TestCreateProfileAllowsEmptyCategoryAndStartURL(t *testing.T) {
	app := &App{
		profiles: []BrowserProfile{},
		dataDir:  t.TempDir(),
	}

	profile, err := app.CreateProfile("空白环境", "", "", "", "   ")
	if err != nil {
		t.Fatalf("CreateProfile returned error: %v", err)
	}

	if profile.Category != "" {
		t.Fatalf("Category = %q, want empty", profile.Category)
	}
	if profile.StartURL != "" {
		t.Fatalf("StartURL = %q, want empty", profile.StartURL)
	}
}

func TestUpdateProfileNormalizesCategory(t *testing.T) {
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
		Category: "  电商   测试 ",
	})
	if err != nil {
		t.Fatalf("UpdateProfile returned error: %v", err)
	}

	if got := app.profiles[0].Category; got != "电商 测试" {
		t.Fatalf("Category = %q, want %q", got, "电商 测试")
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

func TestHandleAutomationProfilesReturnsCategories(t *testing.T) {
	app := &App{
		profiles: []BrowserProfile{
			{ID: "profile-1", Name: "Profile A", Category: "工作", StartURL: "https://chatgpt.com", Platform: "Windows"},
			{ID: "profile-2", Name: "Profile B", Category: "", StartURL: "", Platform: "Windows"},
		},
		automationConfig: AutomationConfig{
			Enabled:       true,
			APIListenAddr: "127.0.0.1:9090",
			APIToken:      "secret-token",
		},
		automationSessions: map[string]*AutomationSession{},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/automation/profiles", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rec := httptest.NewRecorder()

	app.handleAutomationProfiles(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var payload automationResponse
	if err := json.NewDecoder(strings.NewReader(rec.Body.String())).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	dataBytes, err := json.Marshal(payload.Data)
	if err != nil {
		t.Fatalf("marshal nested data: %v", err)
	}

	var summaries []AutomationProfileSummary
	if err := json.Unmarshal(dataBytes, &summaries); err != nil {
		t.Fatalf("unmarshal summaries: %v", err)
	}

	if len(summaries) != 2 {
		t.Fatalf("summary count = %d, want 2", len(summaries))
	}
	if summaries[0].Category != "工作" {
		t.Fatalf("first category = %q, want 工作", summaries[0].Category)
	}
	if summaries[1].Category != "" {
		t.Fatalf("second category = %q, want empty", summaries[1].Category)
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

func TestSetAutomationEnabledPersistsConfig(t *testing.T) {
	app := &App{
		dataDir: t.TempDir(),
		automationConfig: AutomationConfig{
			Enabled:       false,
			APIListenAddr: "127.0.0.1:0",
			APIToken:      "secret-token",
		},
		automationSessions: map[string]*AutomationSession{},
		automationRuntimes: map[string]*automationSessionRuntime{},
	}

	if err := app.SetAutomationEnabled(true); err != nil {
		t.Fatalf("SetAutomationEnabled(true) returned error: %v", err)
	}
	defer func() {
		_ = app.SetAutomationEnabled(false)
	}()

	if !app.automationConfig.Enabled {
		t.Fatal("expected automation to be enabled")
	}
	if app.automationListenAddr == "" {
		t.Fatal("expected automation listen address to be assigned")
	}

	assertAutomationConfig(t, filepath.Join(app.dataDir, "automation.json"), true)

	if err := app.SetAutomationEnabled(false); err != nil {
		t.Fatalf("SetAutomationEnabled(false) returned error: %v", err)
	}
	if app.automationListenAddr != "" {
		t.Fatalf("expected listen address to be cleared, got %q", app.automationListenAddr)
	}

	assertAutomationConfig(t, filepath.Join(app.dataDir, "automation.json"), false)
}

func TestSyncCookiesReadsCookieDatabase(t *testing.T) {
	app := &App{
		profiles: []BrowserProfile{
			{ID: "profile-1", Name: "Test Profile", Cookies: "[]"},
		},
		dataDir: t.TempDir(),
	}

	dbPath := filepath.Join(app.dataDir, "profiles", "profile-1", "cookies.sqlite")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		t.Fatalf("create profile dir: %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE moz_cookies (
		name TEXT,
		value TEXT,
		host TEXT,
		path TEXT,
		expiry INTEGER,
		isSecure INTEGER,
		isHttpOnly INTEGER
	)`); err != nil {
		t.Fatalf("create moz_cookies: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO moz_cookies (name, value, host, path, expiry, isSecure, isHttpOnly) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"sessionid", "abc123", ".chatgpt.com", "/", 1735689600, 1, 1,
	); err != nil {
		t.Fatalf("insert cookie: %v", err)
	}

	if err := app.SyncCookies("profile-1"); err != nil {
		t.Fatalf("SyncCookies returned error: %v", err)
	}

	var cookies []map[string]interface{}
	if err := json.Unmarshal([]byte(app.profiles[0].Cookies), &cookies); err != nil {
		t.Fatalf("unmarshal synced cookies: %v", err)
	}
	if len(cookies) != 1 {
		t.Fatalf("cookie count = %d, want 1", len(cookies))
	}
	if cookies[0]["name"] != "sessionid" {
		t.Fatalf("cookie name = %v, want sessionid", cookies[0]["name"])
	}
}

func TestSyncCookiesReturnsErrorWhenDatabaseMissing(t *testing.T) {
	app := &App{
		profiles: []BrowserProfile{
			{ID: "profile-1", Name: "Test Profile", Cookies: "[]"},
		},
		dataDir: t.TempDir(),
	}

	err := app.SyncCookies("profile-1")
	if err == nil {
		t.Fatal("expected missing cookie database to return error")
	}
	if !strings.Contains(err.Error(), "尚未生成 Cookie 数据库") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExportProfileBundleIncludesMetadataAndFiles(t *testing.T) {
	app := &App{
		dataDir: t.TempDir(),
	}
	profile := BrowserProfile{
		ID:       "profile-1",
		Name:     "Export Target",
		Category: "工作",
		StartURL: "https://chatgpt.com",
		Cookies:  "[]",
		Platform: "Windows",
	}

	userDataDir := filepath.Join(app.dataDir, "profiles", profile.ID)
	if err := os.MkdirAll(filepath.Join(userDataDir, "sessionstore-backups"), 0755); err != nil {
		t.Fatalf("create export profile dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userDataDir, "cookies.sqlite"), []byte("sqlite"), 0644); err != nil {
		t.Fatalf("write cookies.sqlite: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userDataDir, "sessionstore-backups", "recovery.json"), []byte(`{"ok":true}`), 0644); err != nil {
		t.Fatalf("write recovery.json: %v", err)
	}

	bundlePath := filepath.Join(t.TempDir(), "profile.mbp")
	if err := app.exportProfileBundle(profile, bundlePath); err != nil {
		t.Fatalf("exportProfileBundle returned error: %v", err)
	}

	zipReader, err := zip.OpenReader(bundlePath)
	if err != nil {
		t.Fatalf("open bundle zip: %v", err)
	}
	defer zipReader.Close()

	var names []string
	var metadata BrowserProfile
	for _, file := range zipReader.File {
		names = append(names, file.Name)
		if file.Name != "metadata.json" {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			t.Fatalf("open metadata: %v", err)
		}
		if err := json.NewDecoder(reader).Decode(&metadata); err != nil {
			reader.Close()
			t.Fatalf("decode metadata: %v", err)
		}
		reader.Close()
	}

	if metadata.Category != "工作" {
		t.Fatalf("metadata category = %q, want 工作", metadata.Category)
	}
	if metadata.StartURL != "https://chatgpt.com" {
		t.Fatalf("metadata start_url = %q, want https://chatgpt.com", metadata.StartURL)
	}
	if !containsString(names, "data/cookies.sqlite") {
		t.Fatalf("expected cookies.sqlite in bundle, got %v", names)
	}
	if !containsString(names, "data/sessionstore-backups/recovery.json") {
		t.Fatalf("expected nested data file in bundle, got %v", names)
	}
}

func TestImportProfileBundleDefaultsMissingFieldsAndUniqName(t *testing.T) {
	app := &App{
		dataDir: t.TempDir(),
		profiles: []BrowserProfile{
			{ID: "existing", Name: "导入环境"},
		},
	}

	bundlePath := filepath.Join(t.TempDir(), "import.mbp")
	err := writeTestProfileBundle(bundlePath, BrowserProfile{
		ID:       "legacy-profile",
		Name:     "导入环境",
		Category: "  AI   Tools ",
		StartURL: "chatgpt.com",
	}, map[string]string{
		"data/cookies.sqlite": "sqlite",
	})
	if err != nil {
		t.Fatalf("write test bundle: %v", err)
	}

	profile, err := app.importProfileBundle(bundlePath)
	if err != nil {
		t.Fatalf("importProfileBundle returned error: %v", err)
	}

	if profile.ID == "legacy-profile" {
		t.Fatal("expected imported profile to receive a new ID")
	}
	if profile.Name != "导入环境 (1)" {
		t.Fatalf("Name = %q, want 导入环境 (1)", profile.Name)
	}
	if profile.Category != "AI Tools" {
		t.Fatalf("Category = %q, want AI Tools", profile.Category)
	}
	if profile.StartURL != "https://chatgpt.com" {
		t.Fatalf("StartURL = %q, want https://chatgpt.com", profile.StartURL)
	}
	if profile.Platform != "Windows" {
		t.Fatalf("Platform = %q, want Windows", profile.Platform)
	}
	if profile.Cookies != "[]" {
		t.Fatalf("Cookies = %q, want []", profile.Cookies)
	}

	extractedPath := filepath.Join(app.dataDir, "profiles", profile.ID, "cookies.sqlite")
	assertFileContent(t, extractedPath, "sqlite")
}

func TestImportProfileBundleRejectsPathTraversal(t *testing.T) {
	app := &App{
		dataDir:  t.TempDir(),
		profiles: []BrowserProfile{},
		proxies:  []ProxyEntry{},
	}

	bundlePath := filepath.Join(t.TempDir(), "evil.mbp")
	err := writeTestProfileBundle(bundlePath, BrowserProfile{
		ID:   "legacy-profile",
		Name: "恶意环境",
	}, map[string]string{
		"data/../escape.txt": "oops",
	})
	if err != nil {
		t.Fatalf("write malicious bundle: %v", err)
	}

	_, err = app.importProfileBundle(bundlePath)
	if err == nil {
		t.Fatal("expected path traversal bundle to be rejected")
	}
	if !strings.Contains(err.Error(), "非法路径") && !strings.Contains(err.Error(), "越界路径") {
		t.Fatalf("unexpected error: %v", err)
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

func assertAutomationConfig(t *testing.T, path string, wantEnabled bool) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read automation config: %v", err)
	}

	var config AutomationConfig
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("unmarshal automation config: %v", err)
	}

	if config.Enabled != wantEnabled {
		t.Fatalf("automation enabled = %v, want %v", config.Enabled, wantEnabled)
	}
	if strings.TrimSpace(config.APIToken) == "" {
		t.Fatal("expected automation token to be persisted")
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func writeTestProfileBundle(path string, metadata BrowserProfile, files map[string]string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := zip.NewWriter(file)
	defer writer.Close()

	metadataFile, err := writer.Create("metadata.json")
	if err != nil {
		return err
	}
	metadataBytes, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	if _, err := metadataFile.Write(metadataBytes); err != nil {
		return err
	}

	for name, content := range files {
		entry, err := writer.Create(name)
		if err != nil {
			return err
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			return err
		}
	}

	return nil
}
