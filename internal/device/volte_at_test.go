package device

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yibaiba/hideck/internal/config"
)

func TestUseQDC507ATVoiceInPureQMI(t *testing.T) {
	usbPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(usbPath, "product"), []byte("Baiwang\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	pool := &Pool{}
	worker := &Worker{Config: config.DeviceConfig{USBPath: usbPath}}
	if worker.Modem != nil {
		t.Fatal("test requires a pure-QMI worker without a resident AT manager")
	}
	if !pool.useQDC507ATVoice(worker) {
		t.Fatal("QDC507 must use AT voice control even without a resident AT manager")
	}
}

func TestValidateQDC507VoiceATResponse(t *testing.T) {
	tests := []struct {
		name     string
		response string
		wantErr  bool
	}{
		{name: "ok", response: "\r\nOK\r\n"},
		{name: "connect", response: "\r\nCONNECT\r\n"},
		{name: "error", response: "\r\nERROR\r\n", wantErr: true},
		{name: "no carrier", response: "\r\nNO CARRIER\r\n", wantErr: true},
		{name: "empty", response: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateQDC507VoiceATResponse("ATA", tt.response)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error=%v wantErr=%v", err, tt.wantErr)
			}
		})
	}
}
