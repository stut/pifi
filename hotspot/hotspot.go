package hotspot

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stut/pifi/hotspot/connectivitycheck"
	"github.com/stut/pifi/netbackend"
	"github.com/stut/pifi/utils"
)

// Hotspot is the lifecycle module: Start, Stop, IsActive, ConfigureWifi.
// The "when should the hotspot be running?" decision lives in Supervisor —
// Hotspot only knows how to start and stop the AP through its backend.
type Hotspot struct {
	// ConnectivityCheck is the connectivity checker to use.
	ConnectivityCheck *connectivitycheck.ConnectivityCheck `yaml:"connectivity_check"`
	// ConnectivityCheckInterval is how frequently the supervisor should
	// check whether the AP needs starting or stopping.
	ConnectivityCheckInterval time.Duration `yaml:"connectivity_check_interval"`

	// SSID is the SSID of the hotspot; a '*' is replaced with 6 random
	// alphanumeric characters at Init time.
	SSID string `yaml:"ssid"`
	// Password is the password of the hotspot; a '*' is replaced with 16
	// random alphanumeric characters at Init time.
	Password string `yaml:"password"`
	// Channel is the channel of the hotspot.
	Channel int `yaml:"channel"`
	// WirelessInterface is the interface to use for the LAN connection.
	WirelessInterface string `yaml:"wireless_interface"`

	// CommandOnStartHotspot is a command to run when the hotspot is started.
	CommandOnStartHotspot string `yaml:"on_start_hotspot"`
	// CommandOnWifiConnection is a command to run when the hotspot is stopped.
	CommandOnWifiConnection string `yaml:"on_stop_hotspot"`

	// CommandRetryInterval is how frequently the above commands will be retried if they fail.
	CommandRetryInterval time.Duration `yaml:"command_retry_interval"`
	// CommandRetryAttempts is how many times the above commands will be retried before giving up.
	CommandRetryAttempts int `yaml:"command_retry_attempts"`
	// CommandTimeout is how long the above commands will be allowed to run before being killed.
	CommandTimeout time.Duration `yaml:"command_timeout"`

	shell   utils.Shell               `yaml:"-"`
	backend netbackend.NetworkBackend `yaml:"-"`

	// listener, if set, is started after the AP comes up and stopped before
	// it goes down. Used to bind the captive portal's lifetime to the AP's.
	listener Listener `yaml:"-"`

	// lifecycle serialises Start and Stop, which are called from both the
	// supervisor and the captive portal's ConfigureWifi request, and guards
	// listenerRunning.
	lifecycle       sync.Mutex `yaml:"-"`
	listenerRunning bool       `yaml:"-"`

	// configuring is set while ConfigureWifi is in flight so the supervisor
	// can skip its periodic decisions — otherwise it'll see "no gateway, AP
	// down" mid-activation and try to start the AP, interrupting nmcli.
	configuring atomic.Bool `yaml:"-"`
}

// Listener represents something whose lifetime is bound to the AP's: it
// starts after StartHotspot succeeds and stops before StopHotspot runs.
// A Start error is returned from Hotspot.Start, and Start is retried on the
// next call, because an AP without its portal is useless to the user. A Stop
// error is only logged so it can never keep the AP up.
type Listener interface {
	Start() error
	Stop() error
}

// NewHotspot creates a new Hotspot instance with the default values.
func NewHotspot() *Hotspot {
	return &Hotspot{
		ConnectivityCheck:         connectivitycheck.NewConnectivityCheck(),
		ConnectivityCheckInterval: time.Minute,
		SSID:                      "PiFi-*",
		Password:                  "*",
		Channel:                   7,
		WirelessInterface:         "wlan0",
		CommandRetryInterval:      10 * time.Second,
		CommandRetryAttempts:      3,
		CommandTimeout:            5 * time.Second,
	}
}

// Init resolves runtime config (SSID/Password '*' substitution), wires the
// shell into the connectivity check, and detects the appropriate networking
// backend. Every OS call below this point goes through the supplied Shell.
func (h *Hotspot) Init(shell utils.Shell) error {
	utils.Assert(h != nil, "hotspot is nil")

	h.shell = shell
	h.SSID = strings.Replace(h.SSID, "*", utils.RandomString(6), 1)
	h.Password = strings.Replace(h.Password, "*", utils.RandomString(16), 1)
	// WPA2 passphrases are 8 to 63 characters; anything else makes the AP
	// fail to start much later with a far less obvious error.
	if h.Password != "" && (len(h.Password) < 8 || len(h.Password) > 63) {
		return fmt.Errorf("hotspot password must be 8 to 63 characters (or empty for an open hotspot), got %d; note a '*' expands to 16 characters", len(h.Password))
	}

	// A ticker panics on a non-positive interval.
	if h.ConnectivityCheckInterval <= 0 {
		slog.Warn("Invalid connectivity_check_interval, using 1m", "configured", h.ConnectivityCheckInterval)
		h.ConnectivityCheckInterval = time.Minute
	}

	backend, err := netbackend.DetectBackend(shell, netbackend.HotspotConfig{
		SSID:              h.SSID,
		Password:          h.Password,
		Channel:           h.Channel,
		WirelessInterface: h.WirelessInterface,
	})
	if err != nil {
		return err
	}
	h.backend = backend
	// A "connectivity_check:" key with no value in the config file unmarshals
	// to nil, replacing the default.
	if h.ConnectivityCheck == nil {
		h.ConnectivityCheck = connectivitycheck.NewConnectivityCheck()
	}
	h.ConnectivityCheck.SetShell(shell)
	if err := h.ConnectivityCheck.ValidateStatusFileMode(); err != nil {
		return err
	}
	slog.Info("Hotspot initialised")

	return nil
}

// SetListener registers a Listener whose lifetime should be bound to the
// AP's. May be called once before Start.
func (h *Hotspot) SetListener(l Listener) {
	h.listener = l
}

// RestoreConfigFiles asks the backend to undo any leftover config-file
// changes. Optional Controller behaviour the supervisor uses while
// connectivity is healthy.
func (h *Hotspot) RestoreConfigFiles() error {
	return h.backend.RestoreConfigFiles()
}

// IsConfiguringWifi reports whether ConfigureWifi is currently in flight.
// The supervisor uses this to avoid racing nmcli with
// a concurrent decision to (re)start the AP.
func (h *Hotspot) IsConfiguringWifi() bool {
	return h.configuring.Load()
}
