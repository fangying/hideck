package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/yibaiba/hideck/internal/config"
)

func TestGetNotificationSettingsMasksBarkURLsAndIcon(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const secretURL = "https://push.example.invalid/device-secret"
	const secretIcon = "https://assets.example.invalid/private-icon.png?token=secret"
	server := &Server{fullCfg: &config.Config{Bark: config.BarkConfig{
		Enabled: true, URLs: []string{secretURL}, Group: "hideck", Icon: secretIcon, Level: "active",
	}}}

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/settings/notifications", nil)
	server.handleGetNotificationSettings(context)

	if strings.Contains(recorder.Body.String(), "device-secret") || strings.Contains(recorder.Body.String(), "private-icon") {
		t.Fatalf("response leaks Bark configuration: %s", recorder.Body.String())
	}
	var response notificationSettingsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Bark.URLs) != 1 || response.Bark.URLs[0] != notificationSecretMask {
		t.Fatalf("masked URLs = %#v", response.Bark.URLs)
	}
	if response.Bark.Icon != notificationSecretMask {
		t.Fatalf("masked icon = %q", response.Bark.Icon)
	}
}

func TestUpdateNotificationSettingsPreservesMaskedBarkConfiguration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("server:\n  port: 7575\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const secretURL = "https://push.example.invalid/device-secret"
	const secretIcon = "https://assets.example.invalid/private-icon.png?token=secret"
	server := &Server{configPath: configPath, fullCfg: &config.Config{Bark: config.BarkConfig{
		Enabled: true, URLs: []string{secretURL}, Group: "hideck", Icon: secretIcon, Level: "active",
	}}}
	body := `{"bark":{"enabled":true,"urls":["********"],"group":"hideck","icon":"********","level":"active"}}`
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPut, "/api/settings/notifications", strings.NewReader(body))
	context.Request.Header.Set("Content-Type", "application/json")
	server.handleUpdateNotificationSettings(context)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if len(server.fullCfg.Bark.URLs) != 1 || server.fullCfg.Bark.URLs[0] != secretURL || server.fullCfg.Bark.Icon != secretIcon {
		t.Fatalf("runtime Bark configuration was not preserved")
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "device-secret") || !strings.Contains(string(data), "private-icon") {
		t.Fatal("persisted Bark configuration was not preserved")
	}
}

func TestBarkNotificationTestUsesStoredMaskedURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var calls int
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer provider.Close()
	server := &Server{fullCfg: &config.Config{Bark: config.BarkConfig{
		Enabled: true, URLs: []string{provider.URL + "/device-secret"}, Group: "hideck", Level: "active",
	}}}
	body := `{"enabled":true,"urls":["********"],"group":"hideck","icon":"","level":"active"}`
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/settings/notifications/bark/test", strings.NewReader(body))
	context.Request.Header.Set("Content-Type", "application/json")
	server.handleTestBarkNotification(context)

	if recorder.Code != http.StatusOK || calls != 1 {
		t.Fatalf("status = %d, calls = %d, body = %s", recorder.Code, calls, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "device-secret") {
		t.Fatalf("response leaks Bark URL: %s", recorder.Body.String())
	}
}

func TestResolveBarkURLsRejectsOrphanMask(t *testing.T) {
	if _, err := resolveBarkURLs([]string{notificationSecretMask}, nil); err == nil {
		t.Fatal("orphan secret mask was accepted")
	}
}
