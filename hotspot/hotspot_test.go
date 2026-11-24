package hotspot

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stut/pifi/utils/shelltest"
)

// fakeBackend is a netbackend.NetworkBackend that tracks AP state in memory.
type fakeBackend struct {
	active     bool
	starts     int
	stops      int
	configures int
	// duringConfigure, if set, runs while ConfigureWifi is in flight.
	duringConfigure func()
}

func (b *fakeBackend) StartHotspot() error   { b.starts++; b.active = true; return nil }
func (b *fakeBackend) StopHotspot() error    { b.stops++; b.active = false; return nil }
func (b *fakeBackend) IsHotspotActive() bool { return b.active }
func (b *fakeBackend) ConfigureWifi(string, string) error {
	b.configures++
	if b.duringConfigure != nil {
		b.duringConfigure()
	}
	return nil
}
func (b *fakeBackend) RestoreConfigFiles() error { return nil }

type fakeListener struct {
	startErr error
	starts   int
	stops    int
}

func (l *fakeListener) Start() error { l.starts++; return l.startErr }
func (l *fakeListener) Stop() error  { l.stops++; return nil }

func newTestHotspot(backend *fakeBackend, listener *fakeListener) (*Hotspot, *shelltest.RecordingShell) {
	shell := shelltest.New()
	h := &Hotspot{
		CommandOnStartHotspot:   "start-hook",
		CommandOnWifiConnection: "stop-hook",
		CommandRetryAttempts:    3,
		CommandTimeout:          time.Second,
		shell:                   shell,
		backend:                 backend,
	}
	if listener != nil {
		h.SetListener(listener)
	}
	return h, shell
}

func TestStart_APDown_StartsAPListenerAndHook(t *testing.T) {
	t.Parallel()

	backend, listener := &fakeBackend{}, &fakeListener{}
	h, shell := newTestHotspot(backend, listener)

	require.NoError(t, h.Start())
	require.NoError(t, h.Start(), "second Start is a no-op")

	require.Equal(t, 1, backend.starts)
	require.Equal(t, 1, listener.starts)
	require.Equal(t, []string{"start-hook"}, shell.Commands())
}

func TestStart_APUpPortalDown_RepairsPortalWithoutHook(t *testing.T) {
	t.Parallel()

	backend, listener := &fakeBackend{active: true}, &fakeListener{}
	h, shell := newTestHotspot(backend, listener)

	require.NoError(t, h.Start())

	require.Equal(t, 0, backend.starts, "AP is already up")
	require.Equal(t, 1, listener.starts, "portal must be brought up")
	require.Empty(t, shell.Commands(), "a portal-only repair is not a hotspot start")
}

func TestStart_ListenerFails_ReturnsErrorAndRetriesNextTime(t *testing.T) {
	t.Parallel()

	backend, listener := &fakeBackend{}, &fakeListener{startErr: errors.New("port 80 in use")}
	h, _ := newTestHotspot(backend, listener)

	require.ErrorContains(t, h.Start(), "port 80 in use")

	listener.startErr = nil
	require.NoError(t, h.Start())
	require.Equal(t, 1, backend.starts)
	require.Equal(t, 2, listener.starts)
}

func TestStop_ActiveAP_StopsListenerAPAndRunsHook(t *testing.T) {
	t.Parallel()

	backend, listener := &fakeBackend{}, &fakeListener{}
	h, shell := newTestHotspot(backend, listener)
	require.NoError(t, h.Start())

	require.NoError(t, h.Stop())
	require.NoError(t, h.Stop(), "second Stop is a no-op")

	require.Equal(t, 1, listener.stops)
	require.Equal(t, 1, backend.stops)
	require.Equal(t, []string{"start-hook", "stop-hook"}, shell.Commands())

	// The flag was cleared, so the next Start brings the portal back.
	require.NoError(t, h.Start())
	require.Equal(t, 2, listener.starts)
}

func TestRunHook_RetriesUpToAttemptsAndNeverFailsStart(t *testing.T) {
	t.Parallel()

	h, shell := newTestHotspot(&fakeBackend{}, nil)
	for range 3 {
		shell.Expect("start-hook", shelltest.Response{ExitCode: 1, Err: errors.New("boom")})
	}

	require.NoError(t, h.Start(), "hook failures are logged, not returned")

	calls := shell.Calls()
	require.Len(t, calls, 3, "CommandRetryAttempts bounds the attempts")
	require.Equal(t, time.Second, calls[0].Timeout)
}

func TestRunHook_ZeroSettings_RunsOnceWithDefaultTimeout(t *testing.T) {
	t.Parallel()

	h, shell := newTestHotspot(&fakeBackend{}, nil)
	h.CommandRetryAttempts = 0
	h.CommandTimeout = 0
	shell.Expect("start-hook", shelltest.Response{ExitCode: 1, Err: errors.New("boom")})

	require.NoError(t, h.Start())

	calls := shell.Calls()
	require.Len(t, calls, 1)
	require.Equal(t, defaultCommandTimeout, calls[0].Timeout)
}

func TestRunHook_EmptyCommand_NoOp(t *testing.T) {
	t.Parallel()

	h, shell := newTestHotspot(&fakeBackend{}, nil)
	h.CommandOnStartHotspot = ""

	require.NoError(t, h.Start())
	require.Empty(t, shell.Commands())
}

func TestConfigureWifi_InvalidCredentials_LeavesHotspotAlone(t *testing.T) {
	t.Parallel()

	backend := &fakeBackend{active: true}
	h, shell := newTestHotspot(backend, nil)

	require.Error(t, h.ConfigureWifi("home", "short"))
	require.Error(t, h.ConfigureWifi("", "longenough"))

	require.Equal(t, 0, backend.stops)
	require.Equal(t, 0, backend.configures)
	require.Empty(t, shell.Commands())

	require.NoError(t, h.ConfigureWifi("home", "longenough"))
	require.Equal(t, 1, backend.configures)
}

func TestInit_NilConnectivityCheck_GetsDefault(t *testing.T) {
	t.Parallel()

	h := &Hotspot{SSID: "PiFi-*"}
	require.NoError(t, h.Init(shelltest.New()))
	require.NotNil(t, h.ConnectivityCheck)
	require.Len(t, h.SSID, len("PiFi-")+6)
}

func TestInit_PasswordLength(t *testing.T) {
	t.Parallel()

	for password, ok := range map[string]bool{
		"":                       true,
		"*":                      true, // expands to 16 random characters
		"pw-*":                   true,
		"short":                  false,
		"12345678":               true,
		string(make([]byte, 64)): false,
	} {
		err := (&Hotspot{SSID: "PiFi", Password: password}).Init(shelltest.New())
		if ok {
			require.NoError(t, err, "password %q", password)
		} else {
			require.ErrorContains(t, err, "8 to 63", "password %q", password)
		}
	}
}

func TestInit_DefaultHotspotHasGeneratedPassword(t *testing.T) {
	h := NewHotspot()
	require.NoError(t, h.Init(shelltest.New()))
	require.Len(t, h.Password, 16)
	require.NotContains(t, h.Password, "*")
	other := NewHotspot()
	require.NoError(t, other.Init(shelltest.New()))
	require.NotEqual(t, h.Password, other.Password)
}

func TestConfigureWifi_InFlight_BlocksStartAndSecondAttempt(t *testing.T) {
	t.Parallel()

	backend := &fakeBackend{active: true}
	h, _ := newTestHotspot(backend, &fakeListener{})
	backend.duringConfigure = func() {
		// The supervisor's portal repair and a second form submission both
		// arrive mid-attempt; neither may touch the interface.
		require.NoError(t, h.Start())
		require.Error(t, h.ConfigureWifi("other", "longenough"))
	}

	require.NoError(t, h.ConfigureWifi("home", "longenough"))

	require.Equal(t, 0, backend.starts)
	require.Equal(t, 1, backend.configures)
	require.False(t, h.IsConfiguringWifi())
}

func TestConfigureWifi_Success_RefreshesStatusFile(t *testing.T) {
	t.Parallel()

	h, shell := newTestHotspot(&fakeBackend{active: true}, &fakeListener{})
	h.CommandOnWifiConnection = ""
	h.ConnectivityCheck = scriptedConnectivity(t, shell)
	scriptConnected(shell)

	require.NoError(t, h.ConfigureWifi("home", "longenough"))

	status, err := os.ReadFile(h.ConnectivityCheck.StatusFilename)
	require.NoError(t, err)
	require.Contains(t, string(status), `"internet_connected":true`)
}
