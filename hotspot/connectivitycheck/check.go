package connectivitycheck

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Check checks if this device can connect to the internet
//
// Takes the wifi details of the hotspot so it can write them to the status file if the device can't connect to the
// internet.
func (c *ConnectivityCheck) Check(ssid, password string) (bool, error) {
	c.checkMu.Lock()
	defer c.checkMu.Unlock()

	res, err := c.pingGateway()
	if err != nil {
		err = fmt.Errorf("pinging the gateway: %w", err)
		// Readers of the status file must not be left looking at a stale
		// "connected" from before the lookup started failing.
		c.WriteStatusFile(false, false, ssid, password, err)
		return false, err
	}
	if !res {
		slog.Info("Can't ping the gateway, WiFi is not connected/working")
		c.WriteStatusFile(false, false, ssid, password, nil)
		return false, nil
	}

	// We can connect to the gateway, but can we connect to the internet?
	for i := 0; i < c.RetryCount; i++ {
		if i > 0 {
			<-time.After(c.RetryDelay)
		}

		switch c.Method {
		case CheckMethodPing:
			res, err = c.checkConnectivityPing(c.Config, c.Timeout)
		case CheckMethodHTTP:
			res, err = c.checkConnectivityHTTP()
		default:
			err = fmt.Errorf("unknown connectivity check method: %s", c.Method)
			c.WriteStatusFile(false, false, ssid, password, err)
			return false, err
		}

		if err != nil {
			err = fmt.Errorf("checking connectivity: %w", err)
			c.WriteStatusFile(false, false, ssid, password, err)
			return false, err
		}

		if res {
			c.WriteStatusFile(true, true, "", "", nil)
			return true, nil
		}
	}

	slog.Info("WiFi is connected, no internet connectivity")
	c.WriteStatusFile(true, false, "", "", nil)
	return true, nil
}

// checkConnectivity checks if this device can connect to the internet using ping
func (c *ConnectivityCheck) checkConnectivityPing(address string, timeout time.Duration) (bool, error) {
	slog.Debug("Pinging", "host", address)
	_, _, _, err := c.shell.Run(fmt.Sprintf("ping -c 1 \"%s\"", address), timeout)
	if err != nil {
		slog.Debug("Ping failed", "error", err)
		return false, nil
	}

	slog.Debug("Ping succeeded")
	return true, nil
}

// checkConnectivity checks if this device can connect to the internet using an HTTP GET request
func (c *ConnectivityCheck) checkConnectivityHTTP() (bool, error) {
	slog.Debug("HTTP GET", "url", c.Config)
	client := http.Client{Timeout: c.Timeout}
	resp, err := client.Get(c.Config)
	if err != nil {
		// Like a failed ping, an unreachable URL means "not connected"
		// rather than a failure of the check itself.
		slog.Debug("HTTP GET failed", "error", err)
		return false, nil
	}
	defer resp.Body.Close()

	return resp.StatusCode < 400, nil
}

// getGatewayIP returns the IP address of the gateway
func (c *ConnectivityCheck) pingGateway() (bool, error) {
	exitCode, stdOut, stdErr, err := c.shell.Run("ip route list | grep \"default via\" | cut -d\" \" -f3", 5*time.Second)
	if err != nil {
		return false, fmt.Errorf("getting gateway IP: %w", err)
	}
	if exitCode != 0 {
		return false, fmt.Errorf("getting gateway IP: %s", stdErr)
	}

	// Take the first line only — there may be multiple default routes
	gateway := strings.TrimSpace(strings.SplitN(stdOut, "\n", 2)[0])
	if gateway == "" {
		return false, nil
	}

	// A lost ping must not count as lost WiFi: the caller tears the working
	// connection down and raises the hotspot. Only a gateway that stays
	// silent across every attempt is treated as gone.
	for i := 0; i < max(c.RetryCount, 1); i++ {
		if i > 0 {
			<-time.After(c.RetryDelay)
		}
		if ok, _ := c.checkConnectivityPing(gateway, 5*time.Second); ok {
			return true, nil
		}
	}
	return false, nil
}
