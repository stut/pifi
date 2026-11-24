package hotspot

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stut/pifi/hotspot/connectivitycheck"
	"github.com/stut/pifi/utils/shelltest"
)

// fakeController is a Controller implementation that records Start/Stop
// calls and reports IsActive based on its internal state. Tests assert
// against the recorded action sequence.
type fakeController struct {
	mu              sync.Mutex
	active          bool
	configuringWifi bool
	startErr        error
	stopErr         error
	starts          int
	stops           int
	restores        int
}

func (f *fakeController) Start() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts++
	if f.startErr != nil {
		return f.startErr
	}
	f.active = true
	return nil
}

func (f *fakeController) Stop() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops++
	if f.stopErr != nil {
		return f.stopErr
	}
	f.active = false
	return nil
}

func (f *fakeController) IsActive() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.active
}

func (f *fakeController) IsConfiguringWifi() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.configuringWifi
}

func (f *fakeController) RestoreConfigFiles() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restores++
	return nil
}

// scriptedConnectivity wires a ConnectivityCheck against a recording shell so
// the supervisor's check decisions exercise the real connectivity logic
// against scripted ip-route / ping responses.
func scriptedConnectivity(t *testing.T, shell *shelltest.RecordingShell) *connectivitycheck.ConnectivityCheck {
	t.Helper()
	c := &connectivitycheck.ConnectivityCheck{
		Timeout:        100 * time.Millisecond,
		RetryCount:     1,
		RetryDelay:     0,
		Method:         connectivitycheck.CheckMethodPing,
		Config:         "1.1.1.1",
		StatusFilename: t.TempDir() + "/status.json",
	}
	c.SetShell(shell)
	return c
}

func newSupervisor(t *testing.T, ctrl *fakeController, conn *connectivitycheck.ConnectivityCheck, shell *shelltest.RecordingShell) *Supervisor {
	t.Helper()
	s := NewSupervisor(time.Second)
	s.Controller = ctrl
	s.Connectivity = conn
	s.Shell = shell
	s.SSID = "PiFi-test"
	s.Password = "pw"
	s.ReconnectProbeWait = 0
	return s
}

func scriptConnected(s *shelltest.RecordingShell) {
	s.Expect("ip route list", shelltest.Response{Stdout: "192.168.1.1\n"})
	s.Expect("ping -c 1 \"192.168.1.1\"", shelltest.Response{})
	s.Expect("ping -c 1 \"1.1.1.1\"", shelltest.Response{})
}

func scriptDisconnected(s *shelltest.RecordingShell) {
	s.Expect("ip route list", shelltest.Response{Stdout: ""})
}

func TestPeriodicCheck_DisconnectedAndIdle_StartsHotspot(t *testing.T) {
	t.Parallel()

	shell := shelltest.New()
	scriptDisconnected(shell)

	ctrl := &fakeController{active: false}
	sup := newSupervisor(t, ctrl, scriptedConnectivity(t, shell), shell)

	sup.PeriodicCheck()

	require.Equal(t, 1, ctrl.starts, "should have started the hotspot")
	require.True(t, ctrl.active)
}

func TestPeriodicCheck_ConnectedAndActive_StopsHotspot(t *testing.T) {
	t.Parallel()

	shell := shelltest.New()
	scriptConnected(shell)

	ctrl := &fakeController{active: true}
	sup := newSupervisor(t, ctrl, scriptedConnectivity(t, shell), shell)

	sup.PeriodicCheck()

	require.Equal(t, 1, ctrl.stops, "should have stopped the hotspot")
	require.False(t, ctrl.active)
}

func TestPeriodicCheck_ConnectedAndIdle_RestoresConfigFiles(t *testing.T) {
	t.Parallel()

	shell := shelltest.New()
	scriptConnected(shell)

	ctrl := &fakeController{active: false}
	sup := newSupervisor(t, ctrl, scriptedConnectivity(t, shell), shell)

	sup.PeriodicCheck()

	require.Equal(t, 0, ctrl.starts)
	require.Equal(t, 0, ctrl.stops)
	require.Equal(t, 1, ctrl.restores, "controllers that satisfy restorer should be asked to clean up")
}

func TestPeriodicCheck_ConfiguringWifi_SkipsEntirely(t *testing.T) {
	t.Parallel()

	shell := shelltest.New()
	// No connectivity — periodic check would normally start the AP.
	scriptDisconnected(shell)

	ctrl := &fakeController{active: false, configuringWifi: true}
	sup := newSupervisor(t, ctrl, scriptedConnectivity(t, shell), shell)

	sup.PeriodicCheck()

	require.Equal(t, 0, ctrl.starts, "must not race ConfigureWifi by starting the AP")
	require.Equal(t, 0, ctrl.stops)
	require.Empty(t, shell.Commands(), "must not even probe — gateway lookup itself can race nmcli")
}

func TestPeriodicCheck_DisconnectedAndActive_EnsuresPortal(t *testing.T) {
	t.Parallel()

	shell := shelltest.New()
	scriptDisconnected(shell)

	ctrl := &fakeController{active: true}
	sup := newSupervisor(t, ctrl, scriptedConnectivity(t, shell), shell)

	sup.PeriodicCheck()

	require.Equal(t, 1, ctrl.starts, "Start is what repairs a captive portal that is not running")
	require.Equal(t, 0, ctrl.stops, "wait for the user to configure WiFi via the captive portal")
}

func TestResetToWifiOnStartup_HotspotLeftOn_StopsIt(t *testing.T) {
	t.Parallel()

	shell := shelltest.New()
	ctrl := &fakeController{active: true}
	sup := newSupervisor(t, ctrl, scriptedConnectivity(t, shell), shell)

	sup.resetToWifiOnStartup()

	require.Equal(t, 1, ctrl.stops)
	require.Equal(t, 0, ctrl.starts)
	require.Equal(t, 0, ctrl.restores, "Stop already undoes the backend's changes")
	require.Empty(t, shell.Commands(), "must happen before the first connectivity check")
}

func TestResetToWifiOnStartup_HotspotOff_RestoresConfigFiles(t *testing.T) {
	t.Parallel()

	shell := shelltest.New()
	ctrl := &fakeController{active: false}
	sup := newSupervisor(t, ctrl, scriptedConnectivity(t, shell), shell)

	sup.resetToWifiOnStartup()

	require.Equal(t, 1, ctrl.restores)
	require.Equal(t, 0, ctrl.stops)
	require.Equal(t, 0, ctrl.starts)
}

func TestResetToWifiOnStartup_StopFails_CarriesOn(t *testing.T) {
	t.Parallel()

	shell := shelltest.New()
	ctrl := &fakeController{active: true, stopErr: errors.New("stop failed")}
	sup := newSupervisor(t, ctrl, scriptedConnectivity(t, shell), shell)

	require.NotPanics(t, sup.resetToWifiOnStartup)
	require.Equal(t, 1, ctrl.stops)
}

func TestRun_HotspotLeftOn_StopsItAndStaysOnWifi(t *testing.T) {
	t.Parallel()

	shell := shelltest.New()
	// The startup wait sees a default gateway once the AP has let go of the
	// radio, then the first periodic check finds full connectivity.
	shell.Expect("ip route list", shelltest.Response{Stdout: "default via 192.168.1.1 dev wlan0\n"})
	scriptConnected(shell)

	ctrl := &fakeController{active: true}
	sup := newSupervisor(t, ctrl, scriptedConnectivity(t, shell), shell)
	sup.StartupGracePeriod = time.Second
	sup.StartupCheckInterval = time.Millisecond
	sup.CheckInterval = time.Hour

	go sup.Run()
	// Run only takes the kill signal after the first check, so a clean
	// shutdown also means the startup sequence has finished.
	require.NoError(t, sup.Shutdown(5*time.Second))

	require.Equal(t, 1, ctrl.stops, "the leftover hotspot is stopped exactly once")
	require.Equal(t, 0, ctrl.starts, "WiFi came back, so the hotspot must stay down")
	require.False(t, ctrl.IsActive())
	require.Zero(t, shell.PendingExpectations(), "the first check must have run")
}

func TestRun_HotspotLeftOn_NoWifi_RestartsHotspotAfterGrace(t *testing.T) {
	t.Parallel()

	shell := shelltest.New() // every gateway lookup comes back empty

	ctrl := &fakeController{active: true}
	sup := newSupervisor(t, ctrl, scriptedConnectivity(t, shell), shell)
	sup.StartupGracePeriod = 20 * time.Millisecond
	sup.StartupCheckInterval = 5 * time.Millisecond
	sup.CheckInterval = time.Hour

	go sup.Run()
	require.NoError(t, sup.Shutdown(5*time.Second))

	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()
	require.Equal(t, 1, ctrl.starts, "no WiFi after the grace period, so the hotspot comes back")
	// One stop for the startup reset, one for the shutdown.
	require.Equal(t, 2, ctrl.stops)
}

func TestShutdown_RunNotReceiving_HonoursTimeout(t *testing.T) {
	t.Parallel()

	shell := shelltest.New()
	sup := newSupervisor(t, &fakeController{}, scriptedConnectivity(t, shell), shell)

	done := make(chan error, 1)
	go func() { done <- sup.Shutdown(10 * time.Millisecond) }()

	select {
	case err := <-done:
		require.ErrorIs(t, err, errShutdownTimeout)
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown blocked past its timeout")
	}
}

func TestProbeForReconnect_PortalRecentlyActive_Skips(t *testing.T) {
	t.Parallel()

	shell := shelltest.New()
	ctrl := &fakeController{active: true}
	sup := newSupervisor(t, ctrl, scriptedConnectivity(t, shell), shell)
	sup.ReconnectProbeIdleThreshold = time.Hour
	sup.PortalLastActivity = func() time.Time { return time.Now() }

	sup.ProbeForReconnect()

	require.Equal(t, 0, ctrl.stops, "must not stop the AP while portal is in use")
}

func TestProbeForReconnect_ReconnectsSucceeds_StaysDown(t *testing.T) {
	t.Parallel()

	shell := shelltest.New()
	scriptConnected(shell) // probe finds connectivity after Stop()

	ctrl := &fakeController{active: true}
	sup := newSupervisor(t, ctrl, scriptedConnectivity(t, shell), shell)

	sup.ProbeForReconnect()

	require.Equal(t, 1, ctrl.stops, "should stop the AP to free the radio")
	require.Equal(t, 0, ctrl.starts, "connectivity restored — AP stays down")
}

func TestProbeForReconnect_ReconnectFails_RestartsHotspot(t *testing.T) {
	t.Parallel()

	shell := shelltest.New()
	scriptDisconnected(shell) // probe still finds nothing after Stop()

	ctrl := &fakeController{active: true}
	sup := newSupervisor(t, ctrl, scriptedConnectivity(t, shell), shell)

	sup.ProbeForReconnect()

	require.Equal(t, 1, ctrl.stops)
	require.Equal(t, 1, ctrl.starts, "no connectivity — bring the AP back up")
}

func TestProbeForReconnect_StopFails_DoesNotProceed(t *testing.T) {
	t.Parallel()

	shell := shelltest.New()
	ctrl := &fakeController{
		active:  true,
		stopErr: errors.New("stop failed"),
	}
	sup := newSupervisor(t, ctrl, scriptedConnectivity(t, shell), shell)

	sup.ProbeForReconnect()

	require.Equal(t, 1, ctrl.stops)
	require.Equal(t, 0, ctrl.starts, "must not call Start when Stop failed — radio state is unknown")
}
