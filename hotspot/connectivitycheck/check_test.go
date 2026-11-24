package connectivitycheck

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stut/pifi/utils/shelltest"
)

func newTestCheck(t *testing.T, shell *shelltest.RecordingShell) *ConnectivityCheck {
	t.Helper()
	c := &ConnectivityCheck{
		Timeout:        100 * time.Millisecond,
		RetryCount:     1,
		RetryDelay:     0,
		Method:         CheckMethodPing,
		Config:         "1.1.1.1",
		StatusFilename: t.TempDir() + "/status.json",
	}
	c.SetShell(shell)
	return c
}

func TestCheck_NoGateway_ReportsDisconnected(t *testing.T) {
	t.Parallel()

	shell := shelltest.New()
	// `ip route list | grep default via | cut` returns empty — no default route.
	shell.Expect("ip route list", shelltest.Response{Stdout: ""})

	c := newTestCheck(t, shell)
	connected, err := c.Check("PiFi-test", "pw")

	require.NoError(t, err)
	require.False(t, connected)
}

func TestCheck_GatewayPingFails_ReportsDisconnected(t *testing.T) {
	t.Parallel()

	shell := shelltest.New()
	shell.Expect("ip route list", shelltest.Response{Stdout: "192.168.1.1\n"})
	shell.Expect("ping -c 1 \"192.168.1.1\"", shelltest.Response{ExitCode: 1, Err: errPingFailed})

	c := newTestCheck(t, shell)
	connected, err := c.Check("PiFi-test", "pw")

	require.NoError(t, err)
	require.False(t, connected)
}

func TestCheck_GatewayAndInternetReachable_ReportsConnected(t *testing.T) {
	t.Parallel()

	shell := shelltest.New()
	shell.Expect("ip route list", shelltest.Response{Stdout: "192.168.1.1\n"})
	shell.Expect("ping -c 1 \"192.168.1.1\"", shelltest.Response{})
	shell.Expect("ping -c 1 \"1.1.1.1\"", shelltest.Response{})

	c := newTestCheck(t, shell)
	connected, err := c.Check("PiFi-test", "pw")

	require.NoError(t, err)
	require.True(t, connected)
}

func TestCheck_GatewayLookupErrors_WritesStatusFile(t *testing.T) {
	t.Parallel()

	shell := shelltest.New()
	shell.Expect("ip route list", shelltest.Response{ExitCode: -1, Err: stubError("timed out")})

	c := newTestCheck(t, shell)
	connected, err := c.Check("PiFi-test", "pw")

	require.Error(t, err)
	require.False(t, connected)

	b, readErr := os.ReadFile(c.StatusFilename)
	require.NoError(t, readErr, "status file must be written even when the lookup fails")
	var status ConnectivityStatus
	require.NoError(t, json.Unmarshal(b, &status))
	require.False(t, status.GatewayConnected)
	require.False(t, status.InternetConnected)
	require.Equal(t, "PiFi-test", status.HotspotSSID)
	require.Contains(t, status.Error, "timed out")
}

func TestCheckConnectivityHTTP(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/broken" {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))

	c := newTestCheck(t, shelltest.New())
	c.Method = CheckMethodHTTP

	c.Config = srv.URL
	ok, err := c.checkConnectivityHTTP()
	require.NoError(t, err)
	require.True(t, ok)

	c.Config = srv.URL + "/broken"
	ok, err = c.checkConnectivityHTTP()
	require.NoError(t, err)
	require.False(t, ok, "an error status is not connectivity")

	srv.Close()
	c.Config = srv.URL
	ok, err = c.checkConnectivityHTTP()
	require.NoError(t, err)
	require.False(t, ok, "an unreachable URL is not connectivity")
}

// errPingFailed is a sentinel for the ping-failed scenario; only its
// non-nilness matters to the connectivity check.
var errPingFailed = stubError("ping failed")

type stubError string

func (e stubError) Error() string { return string(e) }

func TestCheck_GatewayPingLostOnce_StillConnected(t *testing.T) {
	t.Parallel()

	shell := shelltest.New()
	shell.Expect("ip route list", shelltest.Response{Stdout: "192.168.1.1\n"})
	shell.Expect("ping -c 1 \"192.168.1.1\"", shelltest.Response{ExitCode: 1, Err: errors.New("timed out")})
	shell.Expect("ping -c 1 \"192.168.1.1\"", shelltest.Response{})
	shell.Expect("ping -c 1 \"1.1.1.1\"", shelltest.Response{})

	c := newTestCheck(t, shell)
	c.RetryCount = 3
	connected, err := c.Check("PiFi-test", "pw")

	require.NoError(t, err)
	require.True(t, connected)
}
