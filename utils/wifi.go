package utils

import (
	"errors"
	"strings"
)

// ShellQuote returns s quoted so bash treats it as a single literal word.
// Every value that did not originate in this codebase (SSIDs, passwords,
// interface names from config) must pass through here before being
// interpolated into a Shell.Run command.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ValidateWifiCredentials checks an SSID and passphrase supplied by an
// untrusted client (the captive portal form). An empty password means an open
// network.
//
// ponytail: 64-hex-digit raw PSKs are rejected along with every other
// password longer than 63 bytes; add a hex branch here and unquoted psk
// output in the backends if anyone needs them.
func ValidateWifiCredentials(ssid, password string) error {
	if ssid == "" {
		return errors.New("network name is required")
	}
	if len(ssid) > 32 {
		return errors.New("network name must be at most 32 bytes")
	}
	if password != "" && (len(password) < 8 || len(password) > 63) {
		return errors.New("password must be 8 to 63 characters, or empty for an open network")
	}
	for _, r := range ssid + password {
		if r < 0x20 || r == 0x7f {
			return errors.New("network name and password must not contain control characters")
		}
	}
	return nil
}
