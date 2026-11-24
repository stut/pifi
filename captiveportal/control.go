package captiveportal

import (
	"cmp"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/stut/pifi/netbackend"
)

// Start binds the captive portal's listening socket and serves it in the background. A bind failure (typically
// something else already holding the port) is returned immediately.
func (c *CaptivePortal) Start() error {
	listener, err := net.Listen("tcp", cmp.Or(c.ListenAddress, netbackend.HotspotIPAddress+":80"))
	if err != nil {
		return fmt.Errorf("starting server: %w", err)
	}

	// Create a fresh server each time — http.Server cannot be reused after Close().
	server := &http.Server{
		Handler: c.newHandler(),
		// Don't let idle connections pile up.
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
	}
	c.server = server

	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			slog.Error("Captive portal server error", "error", err)
		}
	}()

	return nil
}

// Stop stops the captive portal web server
func (c *CaptivePortal) Stop() error {
	if c.server == nil {
		return nil
	}
	slog.Info("Stopping captive portal server")
	return c.server.Close()
}
