package netbackend

import (
	"crypto/pbkdf2"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/stut/pifi/utils"
)

const (
	nmConnectionName = "pifi-hotspot"
	nmWifiProfile    = "pifi-wifi"
	// New credentials are tried under this name so a bad submission cannot
	// destroy the saved profile that currently works.
	nmWifiProfileNew  = "pifi-wifi-new"
	nmDnsmasqConfFile = "pifi-captive-portal.conf"
)

// HotspotIPAddress is the local address assigned to the hotspot interface.
const HotspotIPAddress = "10.42.0.1"

// Variables rather than constants so tests can redirect the drop-in directory
// and run the scan loop without waiting.
var (
	nmDnsmasqSharedDir = "/etc/NetworkManager/dnsmasq-shared.d"
	nmScanAttempts     = 10
	nmScanInterval     = 3 * time.Second
)

// nmcli -t separates fields with ':' and escapes literal ':' and '\' with a
// backslash, so scan output has to be unescaped before comparing SSIDs.
var nmTerseUnescaper = strings.NewReplacer(`\\`, `\`, `\:`, `:`)

// NMBackend manages networking using NetworkManager (nmcli).
type NMBackend struct {
	shell utils.Shell
	cfg   HotspotConfig
}

// NewNMBackend creates a new NMBackend that runs every nmcli call through
// the supplied Shell.
func NewNMBackend(shell utils.Shell, cfg HotspotConfig) *NMBackend {
	return &NMBackend{shell: shell, cfg: cfg}
}

// StartHotspot creates and activates a WiFi hotspot using NetworkManager.
// NM handles DHCP and interface management automatically. We add a dnsmasq
// config for DNS wildcarding so captive portal detection works on phones.
func (b *NMBackend) StartHotspot() error {
	// Delete any stale hotspot connection from a previous run. The pifi-wifi
	// profile is deliberately left alone: it is what reconnects the device to
	// the user's network after a reboot or a temporary outage.
	b.deleteConnection()

	// Write dnsmasq config for DNS wildcarding — all DNS queries resolve
	// to the hotspot IP so captive portal detection works on phones.
	if err := b.writeDnsmasqConfig(HotspotIPAddress); err != nil {
		return fmt.Errorf("writing dnsmasq config: %w", err)
	}

	// Build the nmcli command to create a hotspot connection
	cmd := fmt.Sprintf(
		"nmcli connection add type wifi ifname %s con-name %s autoconnect no wifi.mode ap wifi.band bg wifi.channel %d wifi.ssid %s ipv4.method shared ipv4.addresses %s/24",
		utils.ShellQuote(b.cfg.WirelessInterface), nmConnectionName, b.cfg.Channel, utils.ShellQuote(b.cfg.SSID), HotspotIPAddress,
	)

	// Add security settings if a password is provided
	if b.cfg.Password != "" {
		cmd += fmt.Sprintf(" wifi-security.key-mgmt wpa-psk wifi-security.psk %s", utils.ShellQuote(b.cfg.Password))
	}

	slog.Debug("Creating hotspot connection")
	exitCode, _, stdErr, err := b.shell.Run(cmd, 30*time.Second)
	if err != nil {
		// err embeds the full command line, which contains the hotspot
		// password, so it must not reach the logs or the caller.
		stdErr = strings.TrimSpace(stdErr)
		slog.Error("Failed to create hotspot connection", "exit-code", exitCode, "stderr", stdErr)
		b.removeDnsmasqConfig()
		return fmt.Errorf("creating hotspot connection: exit code %d: %s", exitCode, stdErr)
	}

	// Activate the connection
	slog.Debug("Activating hotspot connection")
	exitCode, stdOut, stdErr, err := b.shell.Run(
		fmt.Sprintf("nmcli connection up %s", nmConnectionName), 30*time.Second)
	if err != nil {
		slog.Error("Failed to activate hotspot connection",
			"error", err, "exit-code", exitCode, "stdout", stdOut, "stderr", stdErr)
		// Leave nothing half-started behind.
		b.deleteConnection()
		b.removeDnsmasqConfig()
		return fmt.Errorf("activating hotspot connection: %w", err)
	}

	return nil
}

// StopHotspot deactivates and removes the hotspot connection.
func (b *NMBackend) StopHotspot() error {
	// Deactivate the connection
	slog.Debug("Deactivating hotspot connection")
	exitCode, stdOut, stdErr, err := b.shell.Run(
		fmt.Sprintf("nmcli connection down %s", nmConnectionName), 30*time.Second)
	if err != nil {
		slog.Error("Failed to deactivate hotspot connection",
			"error", err, "exit-code", exitCode, "stdout", stdOut, "stderr", stdErr)
		// Continue to delete even if deactivation fails
	}

	b.deleteConnection()
	b.removeDnsmasqConfig()

	return nil
}

// IsHotspotActive checks if the pifi-hotspot connection is currently active.
func (b *NMBackend) IsHotspotActive() bool {
	_, stdOut, _, err := b.shell.Run(
		"nmcli -t -f NAME connection show --active", 5*time.Second)
	if err != nil {
		slog.Debug("Failed to list active connections", "error", err)
		return false
	}

	for _, line := range strings.Split(stdOut, "\n") {
		if strings.TrimSpace(line) == nmConnectionName {
			slog.Debug("Hotspot is active")
			return true
		}
	}

	slog.Debug("Hotspot is not active")
	return false
}

// ConfigureWifi connects to a WiFi network using NetworkManager.
// NM handles the connection immediately.
func (b *NMBackend) ConfigureWifi(ssid, password string) error {
	// After leaving AP mode the interface needs time to scan for networks.
	// Trigger a rescan and wait for the target SSID to appear.
	visible := b.waitForSSID(ssid)

	// Clear out a leftover from an attempt that was interrupted.
	b.deleteProfile(nmWifiProfileNew)

	// Create the connection profile explicitly rather than using
	// "nmcli device wifi connect" which sometimes fails to auto-detect
	// security settings ("802-11-wireless-security.key-mgmt: property is missing").
	// autoconnect yes is what brings WiFi back after a reboot.
	cmd := fmt.Sprintf(
		"nmcli connection add type wifi ifname %s con-name %s autoconnect yes wifi.ssid %s",
		utils.ShellQuote(b.cfg.WirelessInterface), nmWifiProfileNew, utils.ShellQuote(ssid),
	)
	if !visible {
		// Hidden networks never show up in scan results, so a missing SSID
		// is not proof the network is absent. Try it as a hidden network.
		slog.Warn("SSID not seen in scans, treating it as a hidden network", "ssid", ssid)
		cmd += " wifi.hidden yes"
	}
	if password != "" {
		psk, err := wpa2PSK(ssid, password)
		if err != nil {
			return fmt.Errorf("deriving WPA2 key: %w", err)
		}
		cmd += fmt.Sprintf(" wifi-sec.key-mgmt wpa-psk wifi-sec.psk %s wifi-sec.psk-flags 0", psk)
	}

	slog.Info("Creating WiFi connection profile", "ssid", ssid)
	exitCode, _, stdErr, err := b.shell.Run(cmd, 30*time.Second)
	if err != nil {
		// err embeds the full command line, which contains the WiFi
		// password, so it must not reach the logs or the caller.
		stdErr = strings.TrimSpace(stdErr)
		slog.Error("Failed to create WiFi connection profile", "exit-code", exitCode, "stderr", stdErr)
		b.deleteProfile(nmWifiProfileNew)
		return fmt.Errorf("creating WiFi connection: exit code %d: %s", exitCode, stdErr)
	}

	// Activate it
	slog.Info("Activating WiFi connection", "ssid", ssid)
	exitCode, stdOut, stdErr, err := b.shell.Run(
		"nmcli connection up "+nmWifiProfileNew, 30*time.Second)
	if err != nil {
		slog.Error("Failed to activate WiFi connection",
			"error", err, "exit-code", exitCode, "stdout", stdOut, "stderr", stdErr)
		// Without this, wrong credentials would linger as an autoconnect
		// profile that NM keeps retrying. The previous pifi-wifi profile is
		// untouched, so the device can still get back onto its old network.
		b.deleteProfile(nmWifiProfileNew)
		return fmt.Errorf("activating WiFi connection: %w", err)
	}

	// The new credentials work, so they replace the saved profile.
	b.deleteProfile(nmWifiProfile)
	exitCode, _, stdErr, err = b.shell.Run(
		fmt.Sprintf("nmcli connection modify %s connection.id %s", nmWifiProfileNew, nmWifiProfile), 10*time.Second)
	if err != nil {
		// The connection is up and will keep autoconnecting under the
		// temporary name until the next configure attempt replaces it.
		slog.Error("Failed to rename WiFi connection profile", "exit-code", exitCode, "stderr", stdErr)
	}

	return nil
}

// wpa2PSK derives the 256-bit WPA2 key from a passphrase, as hex.
//
// Handing NetworkManager the passphrase lets it also offer WPA3 (SAE) on
// radios that claim to support it, and on a mixed WPA2/WPA3 network the Pi
// Zero 2 W then times out authenticating and never joins. WPA3 cannot be done
// with a pre-derived key, so this pins the connection to WPA2, which every
// supported board handles. It also keeps the passphrase itself off disk.
//
// ponytail: WPA3-only networks cannot be joined; retry with the passphrase on
// failure if that is ever needed.
func wpa2PSK(ssid, passphrase string) (string, error) {
	key, err := pbkdf2.Key(sha1.New, passphrase, []byte(ssid), 4096, 32)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(key), nil
}

func (b *NMBackend) deleteProfile(name string) {
	b.shell.Run("nmcli connection delete "+name, 10*time.Second)
}

// waitForSSID triggers WiFi scans and reports whether the target SSID showed
// up in any of them.
func (b *NMBackend) waitForSSID(ssid string) bool {
	iface := utils.ShellQuote(b.cfg.WirelessInterface)
	for i := 0; i < nmScanAttempts; i++ {
		// Trigger a rescan
		b.shell.Run(fmt.Sprintf("nmcli device wifi rescan ifname %s", iface), 10*time.Second)
		time.Sleep(nmScanInterval)

		// Check if SSID is visible
		_, stdOut, _, err := b.shell.Run(
			fmt.Sprintf("nmcli -t -f SSID device wifi list ifname %s", iface), 10*time.Second)
		if err != nil {
			continue
		}

		for _, line := range strings.Split(stdOut, "\n") {
			// Leading and trailing spaces are legal in an SSID, so only the
			// line terminator is stripped.
			if nmTerseUnescaper.Replace(strings.TrimSuffix(line, "\r")) == ssid {
				slog.Debug("SSID found after scan", "ssid", ssid, "attempts", i+1)
				return true
			}
		}

		slog.Debug("SSID not yet visible, rescanning", "ssid", ssid, "attempt", i+1)
	}

	return false
}

// RestoreConfigFiles removes the captive portal DNS drop-in. NetworkManager
// owns the rest of its config, but if pifi died while the hotspot was up the
// drop-in is still there and would wildcard DNS on the next shared connection.
func (b *NMBackend) RestoreConfigFiles() error {
	b.removeDnsmasqConfig()
	return nil
}

// writeDnsmasqConfig writes a dnsmasq config file that makes all DNS queries
// resolve to the hotspot IP. NetworkManager reads files from
// /etc/NetworkManager/dnsmasq-shared.d/ when running in shared mode.
func (b *NMBackend) writeDnsmasqConfig(ipAddress string) error {
	confPath := filepath.Join(nmDnsmasqSharedDir, nmDnsmasqConfFile)

	if err := os.MkdirAll(nmDnsmasqSharedDir, 0755); err != nil {
		return fmt.Errorf("creating dnsmasq shared dir: %w", err)
	}

	content := fmt.Sprintf("address=/#/%s\n", ipAddress)
	if err := os.WriteFile(confPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("writing dnsmasq config: %w", err)
	}

	slog.Debug("Wrote dnsmasq captive portal config", "path", confPath, "ip", ipAddress)
	return nil
}

// removeDnsmasqConfig removes the captive portal dnsmasq config file.
func (b *NMBackend) removeDnsmasqConfig() {
	confPath := filepath.Join(nmDnsmasqSharedDir, nmDnsmasqConfFile)
	if err := os.Remove(confPath); err != nil && !os.IsNotExist(err) {
		slog.Error("Failed to remove dnsmasq config", "path", confPath, "error", err)
	}
}

func (b *NMBackend) deleteConnection() {
	_, _, _, err := b.shell.Run(
		fmt.Sprintf("nmcli connection delete %s", nmConnectionName), 10*time.Second)
	if err != nil {
		slog.Debug("No existing hotspot connection to delete (this is normal)")
	}
}
