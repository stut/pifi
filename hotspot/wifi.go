package hotspot

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/stut/pifi/utils"
)

// ConfigureWifi configures the WiFi network with the given SSID and password.
//
// While in flight, IsConfiguringWifi returns true so the supervisor pauses
// its periodic-check / reconnect-probe decisions — otherwise it sees "no
// gateway, AP down" mid-activation and tries to start the AP, interrupting
// nmcli.
func (h *Hotspot) ConfigureWifi(ssid, password string) error {
	// Every caller passes user-supplied values, so reject bad ones here,
	// before the hotspot is torn down for nothing.
	if err := utils.ValidateWifiCredentials(ssid, password); err != nil {
		return err
	}

	// Anyone in range can submit the form, so overlapping attempts are
	// rejected rather than left to fight over the interface.
	if !h.configuring.CompareAndSwap(false, true) {
		return fmt.Errorf("a WiFi configuration attempt is already in progress")
	}
	defer h.configuring.Store(false)

	slog.Info("Configuring WiFi network", "ssid", ssid)

	// Stop hotspot through the hotspot layer so the listener is torn down
	// (captive portal stops, port 80 freed, etc.) before the AP comes down.
	if h.IsActive() {
		if err := h.Stop(); err != nil {
			slog.Error("Failed to stop hotspot before configuring WiFi", "error", err)
		}
	}

	if err := h.backend.ConfigureWifi(ssid, password); err != nil {
		return fmt.Errorf("configuring WiFi: %w", err)
	}

	// Refresh the status file now rather than at the next periodic check, so
	// its readers stop showing hotspot instructions as soon as WiFi is up.
	// The default route can lag a few seconds behind activation, so retry
	// briefly rather than record a false "not connected".
	for attempt := 0; h.ConnectivityCheck != nil && attempt < 5; attempt++ {
		if attempt > 0 {
			time.Sleep(2 * time.Second)
		}
		connected, err := h.ConnectivityCheck.Check(h.SSID, h.Password)
		if err != nil {
			slog.Warn("Connectivity check after configuring WiFi failed", "error", err)
		}
		if connected {
			break
		}
	}

	return nil
}
