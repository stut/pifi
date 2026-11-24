package pifi

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func loadConfig(t *testing.T, yaml string) *PiFi {
	t.Helper()
	filename := filepath.Join(t.TempDir(), "pifi.yaml")
	require.NoError(t, os.WriteFile(filename, []byte(yaml), 0644))
	p, err := NewFromConfig(filename)
	require.NoError(t, err)
	return p
}

func TestNewFromConfig(t *testing.T) {
	for name, yaml := range map[string]string{
		"empty file":         "",
		"comments only":      "# nothing here\n",
		"null sections":      "hotspot:\ncaptiveportal:\n",
		"null document":      "null\n",
		"unrelated override": "debug: true\n",
	} {
		t.Run(name, func(t *testing.T) {
			p := loadConfig(t, yaml)
			require.Equal(t, "PiFi-*", p.Hotspot.SSID)
			require.Equal(t, "*", p.Hotspot.Password)
			require.Equal(t, "wlan0", p.Hotspot.WirelessInterface)
			require.Equal(t, time.Minute, p.Hotspot.ConnectivityCheckInterval)
			require.NotNil(t, p.Hotspot.ConnectivityCheck)
			require.Equal(t, "0600", p.Hotspot.ConnectivityCheck.StatusFileMode)
			require.Equal(t, "10.42.0.1:80", p.CaptivePortal.ListenAddress)
			require.Equal(t, "https://duckduckgo.com/", p.CaptivePortal.RedirectUrl)
		})
	}

	t.Run("override keeps sibling defaults", func(t *testing.T) {
		p := loadConfig(t, "hotspot:\n  wireless_interface: wlan1\n  connectivity_check:\n    retry_count: 9\n")
		require.Equal(t, "wlan1", p.Hotspot.WirelessInterface)
		require.Equal(t, 7, p.Hotspot.Channel)
		require.Equal(t, 9, p.Hotspot.ConnectivityCheck.RetryCount)
		require.Equal(t, "1.1.1.1", p.Hotspot.ConnectivityCheck.Config)
	})

	t.Run("invalid yaml is an error", func(t *testing.T) {
		filename := filepath.Join(t.TempDir(), "pifi.yaml")
		require.NoError(t, os.WriteFile(filename, []byte("hotspot: [\n"), 0644))
		_, err := NewFromConfig(filename)
		require.Error(t, err)
	})
}
