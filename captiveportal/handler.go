package captiveportal

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/stut/pifi/utils"
)

// lastErrorLifetime is how long a failed attempt keeps being reported on the form.
const lastErrorLifetime = 10 * time.Minute

func (c *CaptivePortal) newHandler() http.Handler {
	mux := http.NewServeMux()

	// Wifi configuration page
	mux.HandleFunc("GET /configure-wifi", c.handleConfigureWifi)
	// Wifi configuration form handler
	mux.HandleFunc("POST /configure-wifi", c.handleConfigureWifiPost)
	// Displayed while the wifi is connecting
	mux.HandleFunc("GET /connecting", c.handleConnecting)
	// Serve static files at /static
	mux.Handle("/static/", c.newStaticHandler())
	// Treat all other requests as hotspot detection
	mux.HandleFunc("/", c.handleHotspotDetect)

	return http.NewCrossOriginProtection().Handler(c.loggingMiddleware(mux))
}

// lastAttempt returns the SSID of the most recent connection attempt and the error it ended with, if any
func (c *CaptivePortal) lastAttempt() (ssid, errorMessage string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.lastErrorAt) > lastErrorLifetime {
		// The failure is old news by now, most likely to someone who never saw the attempt.
		return c.lastSSID, ""
	}
	return c.lastSSID, c.lastError
}

// handleHotspotDetect redirects the user to the configuration form
func (c *CaptivePortal) handleHotspotDetect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/configure-wifi", http.StatusFound)
}

// handleConfigureWifi displays the configure wifi page, including the outcome of the last attempt if it failed
func (c *CaptivePortal) handleConfigureWifi(w http.ResponseWriter, _ *http.Request) {
	ssid, errorMessage := c.lastAttempt()
	c.renderConfigureWifiForm(w, ssid, errorMessage)
}

// renderConfigureWifiForm renders the configure wifi form, passing in an optional error message
//
// The password is deliberately never sent back to other hotspot clients.
func (c *CaptivePortal) renderConfigureWifiForm(w http.ResponseWriter, ssid, errorMessage string) {
	c.templates.Render(w, "configure-wifi.html", map[string]string{
		"Error": errorMessage,
		"SSID":  ssid,
	})
}

// handleConfigureWifiPost processes the wifi configuration form
func (c *CaptivePortal) handleConfigureWifiPost(w http.ResponseWriter, r *http.Request) {
	// An SSID and a passphrase fit many times over; anything bigger is not a form submission.
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	ssid := r.FormValue("ssid")
	password := r.FormValue("password")

	// Anything in radio range can post here and these values end up in root shell commands and config files, so
	// nothing unvalidated goes past this point.
	if err := utils.ValidateWifiCredentials(ssid, password); err != nil {
		if len(ssid) > 32 {
			ssid = ""
		}
		c.renderConfigureWifiForm(w, ssid, err.Error())
		return
	}

	c.mu.Lock()
	c.lastSSID = ssid
	c.lastError = ""
	c.mu.Unlock()

	// Send the response before configuring WiFi — the callback stops the
	// hotspot (and this HTTP server), so the response must be flushed first.
	http.Redirect(w, r, "/connecting?skip-check=true", http.StatusFound)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	go func() {
		if err := c.configureWifiCallback(ssid, password); err != nil {
			slog.Error("Failed to configure WiFi", "error", err)
			// The underlying error can quote the commands that were run, password included, so the user gets a
			// generic message and the detail stays in the log.
			c.mu.Lock()
			c.lastErrorAt = time.Now()
			c.lastError = fmt.Sprintf("Could not connect to %s. Check the network name and password and try again.", ssid)
			c.mu.Unlock()
		}
	}()
}

// handleConnecting checks if we have an internet connection, redirects if we do or displays the connecting page
//
// The connecting page should reload every few seconds to check if the connection is up
func (c *CaptivePortal) handleConnecting(w http.ResponseWriter, r *http.Request) {
	ssid, errorMessage := c.lastAttempt()
	if errorMessage != "" {
		// The attempt has failed, so send the user back to the form where the error is shown rather than leaving
		// them watching a spinner.
		http.Redirect(w, r, "/configure-wifi", http.StatusFound)
		return
	}

	skipCheck := r.FormValue("skip-check") == "true"
	if !skipCheck {
		ok, err := c.haveInternetConnectionCallback()
		if err != nil {
			slog.Error("Checking internet connection", "error", err)
		}
		if ok {
			http.Redirect(w, r, c.RedirectUrl, http.StatusFound)
			return
		}
	}

	if ssid == "" {
		// The template displays "Connecting to {{ .SSID }}". This should never be empty, but set it just in case.
		ssid = "WiFi"
	}
	c.templates.Render(w, "connecting.html", map[string]any{"SSID": ssid, "RedirectURL": c.RedirectUrl})
}
