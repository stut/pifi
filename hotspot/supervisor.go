package hotspot

import (
	"log/slog"
	"strings"
	"time"

	"github.com/stut/pifi/hotspot/connectivitycheck"
	"github.com/stut/pifi/utils"
)

// Default timing for the supervisor's reconnect-probe behaviour. Exported as
// vars (not consts) so tests can shrink them.
var (
	DefaultReconnectProbeInterval      = 15 * time.Minute
	DefaultReconnectProbeIdleThreshold = 2 * time.Minute
	DefaultReconnectProbeWait          = 15 * time.Second
	DefaultStartupGracePeriod          = 30 * time.Second
	DefaultStartupCheckInterval        = 5 * time.Second
)

// Controller is the slice of Hotspot the supervisor needs: lifecycle and a
// flag the supervisor honours to stay out of the way while
// ConfigureWifi is in flight. Tests substitute a fake.
type Controller interface {
	Start() error
	Stop() error
	IsActive() bool
	IsConfiguringWifi() bool
}

// Supervisor decides when the hotspot should be running. It owns the periodic
// connectivity check and the reconnect-probe state machine; it owns no OS
// state of its own — that lives behind Controller and ConnectivityCheck.
//
// The interface is small (Run + Shutdown for production; PeriodicCheck +
// ProbeForReconnect for tests) and the leverage is high: every "should the AP
// be up right now?" decision lives here.
type Supervisor struct {
	Controller   Controller
	Connectivity *connectivitycheck.ConnectivityCheck
	Shell        utils.Shell

	// SSID and Password are passed to the connectivity check's status file
	// when the AP is up so that clients can read them out-of-band.
	SSID     string
	Password string

	// PortalLastActivity, if non-nil, returns the time of the most recent
	// captive-portal request. Used to skip reconnect probes while a user is
	// actively configuring WiFi.
	PortalLastActivity func() time.Time

	CheckInterval               time.Duration
	ReconnectProbeInterval      time.Duration
	ReconnectProbeIdleThreshold time.Duration
	ReconnectProbeWait          time.Duration
	StartupGracePeriod          time.Duration
	StartupCheckInterval        time.Duration

	killChannel   chan struct{}
	killedChannel chan struct{}
}

// NewSupervisor wires a Supervisor with the standard probe timings. Caller
// must still set Controller, Connectivity, Shell, SSID/Password, and
// optionally PortalLastActivity before calling Run.
func NewSupervisor(checkInterval time.Duration) *Supervisor {
	return &Supervisor{
		CheckInterval:               checkInterval,
		ReconnectProbeInterval:      DefaultReconnectProbeInterval,
		ReconnectProbeIdleThreshold: DefaultReconnectProbeIdleThreshold,
		ReconnectProbeWait:          DefaultReconnectProbeWait,
		StartupGracePeriod:          DefaultStartupGracePeriod,
		StartupCheckInterval:        DefaultStartupCheckInterval,
		killChannel:                 make(chan struct{}),
		// Buffered so Run can exit even if Shutdown has already given up.
		killedChannel: make(chan struct{}, 1),
	}
}

// Run is the main supervisor loop. Blocks until Shutdown is called.
func (s *Supervisor) Run() {
	s.resetToWifiOnStartup()
	s.waitForNetworkOnStartup()
	s.PeriodicCheck()

	checkTicker := time.NewTicker(s.CheckInterval)
	defer checkTicker.Stop()

	probeTicker := time.NewTicker(s.ReconnectProbeInterval)
	defer probeTicker.Stop()

	for {
		select {
		case <-checkTicker.C:
			s.PeriodicCheck()

		case <-probeTicker.C:
			if s.Controller.IsActive() {
				s.ProbeForReconnect()
			}

		case <-s.killChannel:
			if s.Controller.IsActive() {
				if err := s.Controller.Stop(); err != nil {
					slog.Error("Failed to stop hotspot", "error", err)
				}
			}
			s.killedChannel <- struct{}{}
			return
		}
	}
}

// Shutdown signals Run to exit and waits up to timeout for it to finish.
func (s *Supervisor) Shutdown(timeout time.Duration) error {
	slog.Warn("Shutting down hotspot supervisor")
	// One timer covers both steps: Run only receives the kill signal between
	// checks, so the send itself can block for as long as a check takes.
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case s.killChannel <- struct{}{}:
	case <-timer.C:
		return errShutdownTimeout
	}
	select {
	case <-s.killedChannel:
		slog.Info("Hotspot supervisor shut down")
		return nil
	case <-timer.C:
		return errShutdownTimeout
	}
}

// resetToWifiOnStartup puts the device back into "trying WiFi" mode before
// the first connectivity decision. If pifi died while the AP was up (crash,
// kill, power cut) the radio would otherwise still be held by the hotspot and
// the OS could never rejoin a known network; the startup grace period that
// follows gives it the chance to.
func (s *Supervisor) resetToWifiOnStartup() {
	if s.Controller.IsActive() {
		slog.Info("Hotspot was left enabled, stopping it to retry WiFi")
		if err := s.Controller.Stop(); err != nil {
			slog.Error("Failed to stop leftover hotspot", "error", err)
		}
		return
	}

	// The AP is down but a previous session may still have left its config
	// changes behind, which can be enough to stop WiFi from connecting.
	if c, ok := s.Controller.(restorer); ok {
		if err := c.RestoreConfigFiles(); err != nil {
			slog.Error("Failed to restore config files", "error", err)
		}
	}
}

// PeriodicCheck runs one iteration of the connectivity-driven start/stop
// decision. Exposed for tests so the decision table can be exercised without
// running the ticker loop.
func (s *Supervisor) PeriodicCheck() {
	if s.Controller.IsConfiguringWifi() {
		slog.Debug("Skipping periodic check, ConfigureWifi in flight")
		return
	}

	connected, err := s.Connectivity.Check(s.SSID, s.Password)
	if err != nil {
		slog.Error("Failed to check connectivity", "error", err)
		return
	}

	isActive := s.Controller.IsActive()

	switch {
	case connected && !isActive:
		// Drop any leftover backups from a previous hotspot session. The
		// Controller owns whether this is meaningful (NM backend no-ops).
		if c, ok := s.Controller.(restorer); ok {
			if restoreErr := c.RestoreConfigFiles(); restoreErr != nil {
				slog.Error("Failed to restore config files", "error", restoreErr)
			}
		}
		slog.Debug("Connected, hotspot not active, nothing to do")

	case !connected && isActive:
		// Wait for the user to configure WiFi via the captive portal. Start
		// leaves a running AP alone but brings the portal back if it is not
		// running, which would otherwise leave the user with nothing to
		// configure WiFi through.
		if err := s.Controller.Start(); err != nil {
			slog.Error("Failed to ensure captive portal is running", "error", err)
		}

	case connected && isActive:
		slog.Info("Got internet connectivity, stopping hotspot")
		if err := s.Controller.Stop(); err != nil {
			slog.Error("Failed to stop hotspot:", "error", err)
		}

	case !connected && !isActive:
		slog.Info("No internet connectivity, starting hotspot")
		if err := s.Controller.Start(); err != nil {
			slog.Error("Failed to start hotspot", "error", err)
		}
	}
}

// ProbeForReconnect briefly stops the hotspot to let the OS try to reconnect
// to a known WiFi network. Restarts the hotspot if connectivity isn't
// restored. Exposed for tests.
func (s *Supervisor) ProbeForReconnect() {
	if s.Controller.IsConfiguringWifi() {
		slog.Debug("Skipping reconnect probe, ConfigureWifi in flight")
		return
	}

	if s.PortalLastActivity != nil {
		lastActivity := s.PortalLastActivity()
		if !lastActivity.IsZero() && time.Since(lastActivity) < s.ReconnectProbeIdleThreshold {
			slog.Debug("Skipping reconnect probe, captive portal recently active")
			return
		}
	}

	slog.Info("Probing for existing WiFi connection")
	s.Connectivity.WriteStatusFile(false, false, s.SSID, s.Password, nil)

	if err := s.Controller.Stop(); err != nil {
		slog.Error("Failed to stop hotspot for reconnect probe", "error", err)
		return
	}

	time.Sleep(s.ReconnectProbeWait)

	connected, err := s.Connectivity.Check(s.SSID, s.Password)
	if err != nil {
		slog.Error("Connectivity check failed during probe", "error", err)
	}

	if connected {
		slog.Info("Reconnect probe succeeded, staying connected")
		return
	}

	slog.Info("Reconnect probe failed, restarting hotspot")
	if err := s.Controller.Start(); err != nil {
		slog.Error("Failed to restart hotspot after probe", "error", err)
	}
}

// waitForNetworkOnStartup waits for a default gateway to appear, indicating
// that WiFi has connected. This prevents the hotspot from starting before the
// OS has had a chance to reach an existing network after boot.
func (s *Supervisor) waitForNetworkOnStartup() {
	slog.Info("Waiting for network connectivity", "grace_period", s.StartupGracePeriod)

	deadline := time.Now().Add(s.StartupGracePeriod)
	for time.Now().Before(deadline) {
		_, stdOut, _, err := s.Shell.Run(`ip route list | grep "default via"`, 5*time.Second)
		if err == nil && strings.TrimSpace(stdOut) != "" {
			slog.Info("Default gateway detected, network appears ready")
			return
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		if remaining > s.StartupCheckInterval {
			remaining = s.StartupCheckInterval
		}
		time.Sleep(remaining)
	}

	slog.Info("Startup grace period elapsed without detecting a default gateway")
}

// restorer is implemented by Controllers that maintain backup config files
// the supervisor should ask them to clean up while connectivity is healthy.
// Optional — controllers without backups simply don't satisfy it.
type restorer interface {
	RestoreConfigFiles() error
}

type shutdownTimeoutError struct{}

func (shutdownTimeoutError) Error() string { return "timeout waiting for hotspot to stop" }

var errShutdownTimeout = shutdownTimeoutError{}
