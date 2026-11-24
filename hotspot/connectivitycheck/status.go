package connectivitycheck

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

type ConnectivityStatus struct {
	Timestamp         time.Time `json:"timestamp"`
	GatewayConnected  bool      `json:"gateway_connected"`
	InternetConnected bool      `json:"internet_connected"`
	HotspotSSID       string    `json:"hotspot_ssid"`
	HotspotPassword   string    `json:"hotspot_password"`
	Error             string    `json:"error"`
}

// WriteStatusFile writes the status of the gateway and internet connectivity to a file
func (c *ConnectivityCheck) WriteStatusFile(gatewayConnected, internetConnected bool, hotspotSSID, hotspotPassword string, err error) {
	status := &ConnectivityStatus{
		Timestamp:         time.Now(),
		GatewayConnected:  gatewayConnected,
		InternetConnected: internetConnected,
		HotspotSSID:       hotspotSSID,
		HotspotPassword:   hotspotPassword,
	}

	if err != nil {
		status.Error = err.Error()
	}

	b, err := json.Marshal(status)
	if err != nil {
		slog.Error("encoding status", "error", err)
		return
	}

	if err := c.writeStatus(b); err != nil {
		slog.Error("writing status file", "error", err)
	}
}

// ValidateStatusFileMode rejects permissions that could expose writable state.
func (c *ConnectivityCheck) ValidateStatusFileMode() error {
	_, err := c.statusFileMode()
	return err
}

func (c *ConnectivityCheck) statusFileMode() (os.FileMode, error) {
	switch c.StatusFileMode {
	case "", "0600":
		return 0600, nil
	case "0640":
		return 0640, nil
	case "0644":
		return 0644, nil
	default:
		return 0, fmt.Errorf("status_file_mode must be \"0600\", \"0640\", or \"0644\"")
	}
}

func (c *ConnectivityCheck) writeStatus(data []byte) error {
	mode, err := c.statusFileMode()
	if err != nil {
		return err
	}
	// A fresh file enforces permissions even when upgrading an existing 0644
	// file. Rename also avoids following a destination symlink or exposing
	// partially written JSON to display apps.
	f, err := os.CreateTemp(filepath.Dir(c.StatusFilename), ".pifi-status-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Chmod(mode); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), c.StatusFilename)
}
