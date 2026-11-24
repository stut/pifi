package setup

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/stut/pifi/utils"
)

const (
	defaultConfigPath  = "/etc/pifi.yaml"
	serviceFilePath    = "/etc/systemd/system/pifi.service"
	defaultInstallPath = "/usr/local/bin/pifi"
)

// Run executes the interactive first-run setup. Every shell command (apt-get,
// systemctl) goes through the supplied Shell.
func Run(shell utils.Shell, wirelessInterface string) error {
	fmt.Println("=== PiFi Setup ===")
	fmt.Println()

	// Check root
	if msg := CheckRoot(); msg != "" {
		fmt.Println(msg)
		return nil
	}

	// Check wireless interface
	if msg := CheckWirelessInterface(wirelessInterface); msg != "" {
		fmt.Println(msg)
		return nil
	}
	fmt.Printf("Wireless interface %q found.\n", wirelessInterface)
	fmt.Println()

	if !isNetworkManagerActive(shell) {
		return fmt.Errorf("NetworkManager is not running; pifi needs Raspberry Pi OS Bookworm or later")
	}
	fmt.Println("NetworkManager detected.")
	fmt.Println()

	// Binary location
	binaryPath, originalPath, err := resolveBinaryPath()
	if err != nil {
		return fmt.Errorf("resolving binary path: %w", err)
	}
	fmt.Println()

	// Write default config
	if err := writeDefaultConfig(); err != nil {
		return err
	}
	fmt.Println()

	// Install systemd service
	unitWritten, err := installService(shell, binaryPath)
	if err != nil {
		return err
	}
	fmt.Println()

	// Offer to delete the original binary if it was copied. A service file
	// the user chose to keep may still point at the original, so deleting it
	// would leave a unit that cannot start.
	if originalPath != "" && !unitWritten {
		fmt.Printf("Keeping the original binary at %s: the existing service file may still reference it.\n", originalPath)
		fmt.Println()
	} else if originalPath != "" {
		if confirm(fmt.Sprintf("Delete the original binary at %s?", originalPath), true) {
			if err := os.Remove(originalPath); err != nil {
				fmt.Printf("Failed to delete %s: %v\n", originalPath, err)
			} else {
				fmt.Printf("Deleted %s.\n", originalPath)
			}
		}
		fmt.Println()
	}

	fmt.Println("=== Setup complete ===")
	fmt.Println()
	fmt.Printf("Configuration: %s\n", defaultConfigPath)
	fmt.Printf("Service:       %s\n", serviceFilePath)
	fmt.Printf("Binary:        %s\n", binaryPath)
	fmt.Println()
	fmt.Println("To start pifi now: sudo systemctl start pifi")
	fmt.Println("To view logs:      journalctl -u pifi -f")

	return nil
}

func isNetworkManagerActive(shell utils.Shell) bool {
	exitCode, _, _, _ := shell.Run("systemctl is-active NetworkManager", 5*time.Second)
	return exitCode == 0
}

// resolveBinaryPath returns (installPath, originalPath, error).
// originalPath is non-empty only if the binary was copied to a new location.
func resolveBinaryPath() (string, string, error) {
	currentPath, err := os.Executable()
	if err != nil {
		return "", "", fmt.Errorf("getting executable path: %w", err)
	}
	currentPath, err = filepath.EvalSymlinks(currentPath)
	if err != nil {
		return "", "", fmt.Errorf("resolving executable symlinks: %w", err)
	}

	if currentPath == defaultInstallPath {
		fmt.Printf("Binary is already at %s.\n", defaultInstallPath)
		return currentPath, "", nil
	}

	fmt.Printf("Binary is currently at: %s\n", currentPath)
	choice := promptChoice("Where should the pifi binary live?", []string{
		fmt.Sprintf("Copy to %s", defaultInstallPath),
		fmt.Sprintf("Keep at %s", currentPath),
	}, 0)

	if choice == 0 {
		fmt.Printf("Copying to %s...\n", defaultInstallPath)
		if err := copyFile(currentPath, defaultInstallPath); err != nil {
			return "", "", fmt.Errorf("copying binary: %w", err)
		}
		return defaultInstallPath, currentPath, nil
	}

	return currentPath, "", nil
}

// copyFile replaces dst with a copy of src via a temp file and rename.
// Writing over dst in place fails with "text file busy" when dst is the
// binary of a running pifi.
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op once renamed
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0755); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

func writeDefaultConfig() error {
	if _, err := os.Stat(defaultConfigPath); err == nil {
		if !confirm(fmt.Sprintf("Config file %s already exists. Overwrite?", defaultConfigPath), false) {
			fmt.Println("Keeping existing config.")
			return nil
		}
	}

	fmt.Printf("Writing default config to %s...\n", defaultConfigPath)
	password, err := writeConfig(defaultConfigPath)
	if err != nil {
		return err
	}
	fmt.Printf("Hotspot password: %s\nSave this password before disconnecting from the Pi.\n", password)
	return nil
}

// writeConfig saves a stable generated password in a root-only config file.
func writeConfig(filename string) (string, error) {
	password := utils.RandomString(16)
	config := strings.Replace(defaultConfig, `password: "*"`, `password: "`+password+`"`, 1)
	f, err := os.CreateTemp(filepath.Dir(filename), ".pifi-config-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err := f.WriteString(config); err != nil {
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(f.Name(), filename); err != nil {
		return "", err
	}
	return password, nil
}

// installService writes and enables the systemd unit. It reports whether the
// unit was written: false means the user kept an existing service file.
func installService(shell utils.Shell, binaryPath string) (bool, error) {
	unit := fmt.Sprintf(serviceTemplate, binaryPath, defaultConfigPath)

	if _, err := os.Stat(serviceFilePath); err == nil {
		if !confirm(fmt.Sprintf("Service file %s already exists. Overwrite?", serviceFilePath), false) {
			fmt.Println("Keeping existing service file.")
			return false, nil
		}
	}

	fmt.Printf("Installing systemd service to %s...\n", serviceFilePath)
	if err := os.WriteFile(serviceFilePath, []byte(unit), 0644); err != nil {
		return false, fmt.Errorf("writing service file: %w", err)
	}

	// Check if systemctl is available before trying to use it
	if _, err := exec.LookPath("systemctl"); err != nil {
		fmt.Println("systemctl not found, skipping daemon-reload and enable.")
		return true, nil
	}

	_, _, _, err := shell.Run("systemctl daemon-reload", 30*time.Second)
	if err != nil {
		return true, fmt.Errorf("reloading systemd: %w", err)
	}

	_, _, _, err = shell.Run("systemctl enable pifi", 30*time.Second)
	if err != nil {
		return true, fmt.Errorf("enabling pifi service: %w", err)
	}
	fmt.Println("Service installed and enabled.")

	return true, nil
}

const serviceTemplate = `[Unit]
Description=PiFi - WiFi configuration via captive portal
After=network.target

[Service]
Type=simple
ExecStart=%s -c %s
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
`

const defaultConfig = `# PiFi configuration
# See https://github.com/stut/pifi for documentation

# Enable debug logging
debug: false

# Output logs in JSON format
jsonlog: false

# Hotspot configuration
hotspot:
  # How often to check for internet connectivity
  connectivity_check_interval: 1m

  # Connectivity check settings
  connectivity_check:
    # Method to check connectivity: "ping" or "http"
    method: ping
    # Target to ping (or URL for http method)
    config: "1.1.1.1"
    # Timeout for each connectivity check attempt
    timeout: 5s
    # Number of retries before considering connectivity lost
    retry_count: 3
    # Delay between retries
    retry_interval: 10s
    # File to write connectivity status to
    status_filename: /run/pifi-connectivity-status.json
    # Contains the hotspot password: root-only unless explicitly shared
    status_file_mode: "0600"

  # Hotspot SSID - a '*' will be replaced with 6 random characters
  ssid: "PiFi-*"
  # Setup saves a generated password here. A '*' generates 16 new characters
  # on each service start. Empty explicitly opts into an insecure open network.
  password: "*"
  # WiFi channel
  channel: 7
  # Wireless interface name
  wireless_interface: wlan0

  # Command to run when the hotspot starts (e.g. stop services that need internet)
  on_start_hotspot: ""
  # Command to run when the hotspot stops (e.g. start services that need internet)
  on_stop_hotspot: ""

# Captive portal configuration
captiveportal:
  # Serve only on the hotspot address, never all interfaces
  listen_address: "10.42.0.1:80"
  # URL to redirect to after WiFi is configured successfully
  redirect_url: "https://duckduckgo.com/"
  # Directory containing custom HTML templates (empty = use built-in templates)
  templates_dir: ""
  # Cache compiled templates
  cache_templates: true
  # Directory containing custom static files (empty = use built-in files)
  static_dir: ""
`
