package connectivitycheck

import (
	"sync"
	"time"

	"github.com/stut/pifi/utils"
)

type Method string

const (
	CheckMethodPing Method = "ping"
	CheckMethodHTTP Method = "http"
)

type ConnectivityCheck struct {
	// Timeout is how long to wait for a wifi connection before starting the hotspot, in seconds
	Timeout time.Duration `yaml:"timeout"`
	// RetryCount is how many times to retry checking for a wifi connection before starting the hotspot
	RetryCount int `yaml:"retry_count"`
	// RetryDelay is how long to wait between retries to check for a wifi connection, in seconds
	RetryDelay time.Duration `yaml:"retry_interval"`
	// Method is the method to use to check for a wifi connection; ping or http get
	Method Method `yaml:"method"`
	// Config is the method-specific configuration for checking for a wifi connection; host or ip for ping, url
	// for http
	Config string `yaml:"config"`

	// StatusFilename is the filename to write the status of the connectivity check to
	StatusFilename string `yaml:"status_filename"`
	// StatusFileMode is an octal permission string: 0600 (default), 0640, or 0644.
	StatusFileMode string `yaml:"status_file_mode"`

	// shell runs every shell command issued by the connectivity check
	// (gateway lookup, ping). Set via SetShell before Check is called.
	shell utils.Shell `yaml:"-"`

	// checkMu serialises Check: the supervisor and every captive portal page
	// load call it, and concurrent runs would interleave status file writes.
	checkMu sync.Mutex `yaml:"-"`
}

// NewConnectivityCheck creates a new ConnectivityCheck instance with the default values
func NewConnectivityCheck() *ConnectivityCheck {
	return &ConnectivityCheck{
		Timeout:        5 * time.Second,
		RetryCount:     3,
		RetryDelay:     10 * time.Second,
		Method:         CheckMethodPing,
		Config:         "1.1.1.1",
		StatusFilename: "/run/pifi-connectivity-status.json",
		StatusFileMode: "0600",
	}
}

// SetShell wires the Shell used for gateway lookup and ping. Must be called
// before Check.
func (c *ConnectivityCheck) SetShell(shell utils.Shell) {
	c.shell = shell
}
