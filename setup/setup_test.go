package setup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stut/pifi/pifi"
)

func TestCopyFileReplacesDestinationByRename(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	require.NoError(t, os.WriteFile(src, []byte("new"), 0644))
	require.NoError(t, os.WriteFile(dst, []byte("old"), 0600))
	before, err := os.Stat(dst)
	require.NoError(t, err)

	require.NoError(t, copyFile(src, dst))

	data, err := os.ReadFile(dst)
	require.NoError(t, err)
	require.Equal(t, "new", string(data))
	after, err := os.Stat(dst)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0755), after.Mode().Perm())
	// A rename swaps in a new inode; an in-place write (which fails with
	// "text file busy" on a running binary) would keep the old one.
	require.False(t, os.SameFile(before, after))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 2, "temp file left behind")
}

func TestWriteConfigProtectsAndPersistsGeneratedPassword(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "pifi.yaml")
	require.NoError(t, os.WriteFile(filename, []byte("old config"), 0644))
	password, err := writeConfig(filename)
	require.NoError(t, err)
	require.Len(t, password, 16)
	info, err := os.Stat(filename)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	config, err := pifi.NewFromConfig(filename)
	require.NoError(t, err)
	require.Equal(t, password, config.Hotspot.Password)
	require.Equal(t, "0600", config.Hotspot.ConnectivityCheck.StatusFileMode)
	require.Equal(t, "10.42.0.1:80", config.CaptivePortal.ListenAddress)
}
