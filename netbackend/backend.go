package netbackend

import (
	"fmt"
	"time"

	"github.com/stut/pifi/utils"
)

// NetworkBackend is the seam between the hotspot logic and the networking
// stack. NetworkManager is the only implementation; tests substitute a fake.
type NetworkBackend interface {
	// StartHotspot brings up the access point.
	StartHotspot() error

	// StopHotspot tears down the access point and restores normal WiFi operation.
	StopHotspot() error

	// IsHotspotActive reports whether the access point is currently running.
	IsHotspotActive() bool

	// ConfigureWifi connects the device to an infrastructure WiFi network.
	ConfigureWifi(ssid, password string) error

	// RestoreConfigFiles undoes any config file changes made by the backend.
	RestoreConfigFiles() error
}

// HotspotConfig holds the AP-side parameters the backend needs.
type HotspotConfig struct {
	SSID              string
	Password          string
	Channel           int
	WirelessInterface string
}

// DetectBackend returns the NetworkManager backend, or an error on a system
// that is not running NetworkManager (Raspberry Pi OS before Bookworm).
func DetectBackend(shell utils.Shell, cfg HotspotConfig) (NetworkBackend, error) {
	if exitCode, _, _, _ := shell.Run("systemctl is-active NetworkManager", 5*time.Second); exitCode != 0 {
		return nil, fmt.Errorf("NetworkManager is not running; pifi needs Raspberry Pi OS Bookworm or later")
	}
	return NewNMBackend(shell, cfg), nil
}
