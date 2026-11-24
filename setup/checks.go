package setup

import (
	"fmt"
	"os"
)

// CheckRoot verifies the process is running as root.
// Returns a user-friendly message if not root, or empty string if OK.
func CheckRoot() string {
	if os.Geteuid() != 0 {
		return "PiFi must be run as root."
	}
	return ""
}

// CheckWirelessInterface verifies the given wireless interface exists.
// Returns a user-friendly message if not found, or empty string if OK.
func CheckWirelessInterface(iface string) string {
	path := fmt.Sprintf("/sys/class/net/%s/wireless", iface)
	if _, err := os.Stat(path); err != nil {
		return fmt.Sprintf("Wireless interface %q not found.", iface)
	}
	return ""
}
