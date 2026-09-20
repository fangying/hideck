package device

import (
	"os"
	"path/filepath"
	"strings"
)

var qdc507AudioReadyFile = "/run/hideck-qdc507-audio/ready"

// qdc507ResidentAudioReady reports that the module-side D4-to-UAC route has
// passed the external runtime's kernel, ACDB and PCM readiness checks. The
// runtime writes this marker and removes it when the route is stopped.
func qdc507ResidentAudioReady(usbPath string) bool {
	usbPath = resolveUSBPath(usbPath)
	if strings.TrimSpace(usbPath) == "" || !isBaiwangUSB(usbPath) {
		return false
	}
	data, err := os.ReadFile(qdc507AudioReadyFile)
	return err == nil && strings.TrimSpace(string(data)) == "ready"
}

func isBaiwangUSB(usbPath string) bool {
	for _, name := range []string{"product", "manufacturer"} {
		if unusableUACName(readSysfsText(filepath.Join(usbPath, name))) {
			return true
		}
	}
	return false
}
