package deej

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"go.uber.org/zap"
)

func newTestWebConfigServer(t *testing.T) (*WebConfigServer, string) {
	t.Helper()

	configPath := filepath.Join(t.TempDir(), "config.yaml")

	userConfig := viper.New()
	userConfig.SetConfigType(configType)
	userConfig.SetConfigFile(configPath)

	config := &CanonicalConfig{
		logger:         zap.NewNop().Sugar(),
		userConfig:     userConfig,
		internalConfig: viper.New(),
	}

	return &WebConfigServer{
		logger: zap.NewNop().Sugar(),
		config: config,
	}, configPath
}

// the settings page re-reads its configuration right after saving, so a save has to leave the
// canonical config holding the new values instead of waiting for the config file watcher
func TestHandleSaveConfigAppliesValuesImmediately(t *testing.T) {
	server, configPath := newTestWebConfigServer(t)

	body := `{
		"sliderMappings": {"0": "master", "1": "firefox, mpv"},
		"comPort": " /dev/ttyACM0 ",
		"baudRate": 115200,
		"invertSliders": true,
		"noiseReduction": "high"
	}`

	recorder := httptest.NewRecorder()
	server.handleSaveConfig(recorder, httptest.NewRequest(http.MethodPost, "/api/save", strings.NewReader(body)))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recorder.Code)
	}

	var response struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}

	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !response.Success {
		t.Fatalf("expected a successful save, got error %q", response.Error)
	}

	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("expected the config file to be written: %v", err)
	}

	config := server.config

	if config.ConnectionInfo.COMPort != "/dev/ttyACM0" {
		t.Errorf("expected the com port to be trimmed and applied, got %q", config.ConnectionInfo.COMPort)
	}

	if config.ConnectionInfo.BaudRate != 115200 {
		t.Errorf("expected baud rate 115200, got %d", config.ConnectionInfo.BaudRate)
	}

	if !config.InvertSliders {
		t.Error("expected inverted sliders to be applied")
	}

	if config.NoiseReductionLevel != "high" {
		t.Errorf("expected noise reduction 'high', got %q", config.NoiseReductionLevel)
	}

	targets, ok := config.SliderMapping.get(1)
	if !ok || len(targets) != 2 || targets[0] != "firefox" || targets[1] != "mpv" {
		t.Errorf("expected slider 1 to map to [firefox mpv], got %v (found: %v)", targets, ok)
	}
}

func TestHandleSaveConfigRejectsInvalidJSON(t *testing.T) {
	server, _ := newTestWebConfigServer(t)

	recorder := httptest.NewRecorder()
	server.handleSaveConfig(recorder, httptest.NewRequest(http.MethodPost, "/api/save", strings.NewReader("not json")))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", recorder.Code)
	}
}
