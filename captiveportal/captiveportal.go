package captiveportal

import (
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stut/pifi/netbackend"
	"github.com/stut/pifi/utils"
)

type CaptivePortal struct {
	// ListenAddress limits the portal to the hotspot address by default.
	ListenAddress string `yaml:"listen_address"`

	// RedirectURL is the URL to take the user to once an internet connection is established
	RedirectUrl string `yaml:"redirect_url"`

	// TemplatesDir is the directory containing the HTML templates. If not specified the embedded templates will be used
	TemplatesDir string `yaml:"templates_dir"`
	// CacheTemplates specifies whether to cache the templates
	CacheTemplates bool `yaml:"cache_templates"`
	// StaticDir is the directory containing the HTML templates. If not specified the embedded templates will be used
	StaticDir string `yaml:"static_dir"`

	// templates is the parsed templates
	templates *Templates `yaml:"-"`

	// server is the web server
	server *http.Server `yaml:"-"`

	// configureWifiCallback is called with new wifi details when the user submits the form
	configureWifiCallback func(ssid, password string) error `yaml:"-"`
	// haveInternetConnectionCallback is called to check if the internet connection is up
	haveInternetConnectionCallback func() (bool, error) `yaml:"-"`

	// mu guards lastSSID and lastError, which are written by request handlers and by the goroutine running the
	// configure callback
	mu sync.Mutex `yaml:"-"`
	// lastSSID is the SSID of the last network entered into the form - used to prefill the form
	lastSSID string `yaml:"-"`
	// lastError describes why the last connection attempt failed, and is shown on the form. It is empty while an
	// attempt is in flight or after it succeeded.
	lastError string `yaml:"-"`
	// lastErrorAt is when lastError was recorded; it stops being shown after lastErrorLifetime
	lastErrorAt time.Time `yaml:"-"`

	// lastActivity tracks the most recent HTTP request time (unix nanos, atomic)
	lastActivity atomic.Int64 `yaml:"-"`
}

// RecordActivity updates the last activity timestamp
func (c *CaptivePortal) RecordActivity() {
	c.lastActivity.Store(time.Now().UnixNano())
}

// LastActivity returns the time of the last HTTP request
func (c *CaptivePortal) LastActivity() time.Time {
	ns := c.lastActivity.Load()
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, ns)
}

func NewCaptivePortal() *CaptivePortal {
	return &CaptivePortal{
		ListenAddress:  netbackend.HotspotIPAddress + ":80",
		RedirectUrl:    "https://duckduckgo.com/",
		TemplatesDir:   "",
		CacheTemplates: true,
		StaticDir:      "",
	}
}

// Init initialises the captive portal
//
// The configurationCallback is called when the user submits the form with new wifi details.
func (c *CaptivePortal) Init(configureCallback func(ssid, password string) error, haveInternetConnectionCallback func() (bool, error)) error {
	utils.Assert(c != nil, "captive portal is nil")
	utils.Assert(configureCallback != nil, "configureCallback is nil")
	utils.Assert(haveInternetConnectionCallback != nil, "checkConnectionCallback is nil")

	var err error
	c.templates, err = getTemplates(c.TemplatesDir, c.CacheTemplates)
	if err != nil {
		return fmt.Errorf("initializing templates: %w", err)
	}

	c.configureWifiCallback = configureCallback
	c.haveInternetConnectionCallback = haveInternetConnectionCallback

	return nil
}
