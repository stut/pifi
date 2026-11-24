package utils

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestShellQuote_RoundTripsThroughBash(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"plain", "it's", "'; touch /tmp/pwned; '", "$(id) `id` \"x\" \\ * ~", "", "a b\tc"} {
		_, out, _, err := NewBashShell().Run("printf %s "+ShellQuote(s), 5*time.Second)
		require.NoError(t, err)
		require.Equal(t, s, out)
	}
}

func TestValidateWifiCredentials(t *testing.T) {
	t.Parallel()
	require.NoError(t, ValidateWifiCredentials("Home WiFi", "correct horse"))
	require.NoError(t, ValidateWifiCredentials("open-net", ""))
	require.NoError(t, ValidateWifiCredentials("it's; $(reboot)", "pass'word\"$x"))

	require.Error(t, ValidateWifiCredentials("", "password1"))
	require.Error(t, ValidateWifiCredentials(strings.Repeat("a", 33), "password1"))
	require.Error(t, ValidateWifiCredentials("net", "short"))
	require.Error(t, ValidateWifiCredentials("net", strings.Repeat("a", 64)))
	require.Error(t, ValidateWifiCredentials("net\nssid=evil", "password1"))
	require.Error(t, ValidateWifiCredentials("net", "password\n}"))
}

func TestBashShell_TimeoutKillsChildren(t *testing.T) {
	t.Parallel()
	start := time.Now()
	_, _, _, err := NewBashShell().Run("sleep 30 | cat", 200*time.Millisecond)
	require.ErrorContains(t, err, "timed out")
	require.Less(t, time.Since(start), 5*time.Second)
}
