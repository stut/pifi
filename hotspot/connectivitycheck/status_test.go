package connectivitycheck

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStatusFilePermissionsAndSchema(t *testing.T) {
	for mode, want := range map[string]os.FileMode{"": 0600, "0600": 0600, "0640": 0640, "0644": 0644} {
		t.Run(mode, func(t *testing.T) {
			c := NewConnectivityCheck()
			c.StatusFilename = filepath.Join(t.TempDir(), "status.json")
			c.StatusFileMode = mode
			// Existing versions were world-readable; an upgrade must tighten them.
			require.NoError(t, os.WriteFile(c.StatusFilename, []byte("old"), 0644))
			c.WriteStatusFile(false, false, "PiFi-test", "hotspot-password", nil)
			info, err := os.Stat(c.StatusFilename)
			require.NoError(t, err)
			require.Equal(t, want, info.Mode().Perm())
			data, err := os.ReadFile(c.StatusFilename)
			require.NoError(t, err)
			var status map[string]any
			require.NoError(t, json.Unmarshal(data, &status))
			require.Len(t, status, 6)
			require.Equal(t, "hotspot-password", status["hotspot_password"])
			entries, err := os.ReadDir(filepath.Dir(c.StatusFilename))
			require.NoError(t, err)
			require.Len(t, entries, 1, "temporary files must be removed")
		})
	}
}

func TestStatusFileDoesNotFollowSymlinks(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "other-file")
	require.NoError(t, os.WriteFile(target, []byte("untouched"), 0600))
	c := NewConnectivityCheck()
	c.StatusFilename = filepath.Join(dir, "status.json")
	require.NoError(t, os.Symlink(target, c.StatusFilename))
	require.NoError(t, c.writeStatus([]byte(`{}`)))
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "untouched", string(data))
}

func TestInvalidStatusModeDoesNotWrite(t *testing.T) {
	c := NewConnectivityCheck()
	c.StatusFilename = filepath.Join(t.TempDir(), "status.json")
	c.StatusFileMode = "0666"
	require.Error(t, c.ValidateStatusFileMode())
	require.Error(t, c.writeStatus([]byte(`{}`)))
	_, err := os.Stat(c.StatusFilename)
	require.True(t, os.IsNotExist(err))
}
