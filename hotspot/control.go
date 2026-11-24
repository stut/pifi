package hotspot

import (
	"fmt"
	"log/slog"
	"time"
)

// Fallbacks for hook settings left at zero, which happens when the Hotspot was
// not built by NewHotspot or the config file zeroes them.
const (
	defaultCommandTimeout       = 30 * time.Second
	defaultCommandRetryAttempts = 1
)

// Start brings up the access point and its Listener. Idempotent: an AP that
// is already up is left alone, but a Listener that is not running is still
// started, so calling Start again repairs an AP whose captive portal failed
// to come up. A listener failure is returned because an AP without its portal
// gives the user no way to configure WiFi.
func (h *Hotspot) Start() error {
	startedAP, err := h.start()
	if startedAP {
		h.runHook("on_start_hotspot", h.CommandOnStartHotspot)
	}
	return err
}

func (h *Hotspot) start() (startedAP bool, err error) {
	h.lifecycle.Lock()
	defer h.lifecycle.Unlock()

	// Bringing the AP up now would knock the interface out from under the
	// WiFi connection attempt. The supervisor retries once it has finished.
	if h.configuring.Load() {
		return false, nil
	}

	if !h.IsActive() {
		if err := h.backend.StartHotspot(); err != nil {
			return false, fmt.Errorf("starting hotspot: %w", err)
		}
		startedAP = true
	}

	if h.listener != nil && !h.listenerRunning {
		if err := h.listener.Start(); err != nil {
			return startedAP, fmt.Errorf("starting listener: %w", err)
		}
		h.listenerRunning = true
	}

	return startedAP, nil
}

// Stop tears down the access point. Idempotent: returns nil if already down.
// Any registered Listener is stopped first so port 80 (or whatever else the
// listener holds) is released before the AP comes down. Listener stop errors
// are logged but do not abort the AP teardown.
func (h *Hotspot) Stop() error {
	stoppedAP, err := h.stop()
	if stoppedAP {
		h.runHook("on_stop_hotspot", h.CommandOnWifiConnection)
	}
	return err
}

func (h *Hotspot) stop() (stoppedAP bool, err error) {
	h.lifecycle.Lock()
	defer h.lifecycle.Unlock()

	// Stopped even when the AP has already gone away underneath us, so the
	// listener never outlives it.
	if h.listener != nil && h.listenerRunning {
		if err := h.listener.Stop(); err != nil {
			slog.Error("Listener failed to stop", "error", err)
		}
		h.listenerRunning = false
	}

	if !h.IsActive() {
		return false, nil
	}

	if err := h.backend.StopHotspot(); err != nil {
		return false, fmt.Errorf("stopping hotspot: %w", err)
	}

	return true, nil
}

// runHook runs one of the user's hook commands. The command comes from the
// root-owned config file and is deliberately run as-is. Hooks are a courtesy
// to the user, so a failing one is logged and never affects the hotspot.
func (h *Hotspot) runHook(name, command string) {
	if command == "" {
		return
	}

	timeout := h.CommandTimeout
	if timeout <= 0 {
		timeout = defaultCommandTimeout
	}
	attempts := h.CommandRetryAttempts
	if attempts < 1 {
		attempts = defaultCommandRetryAttempts
	}

	for attempt := 1; ; attempt++ {
		exitCode, _, stdErr, err := h.shell.Run(command, timeout)
		if err == nil {
			return
		}
		slog.Error("Hook command failed",
			"hook", name,
			"attempt", attempt,
			"error", err,
			"exit-code", exitCode,
			"stderr", stdErr)
		if attempt >= attempts {
			return
		}
		time.Sleep(h.CommandRetryInterval)
	}
}

// IsActive returns whether the hotspot is currently active.
func (h *Hotspot) IsActive() bool {
	return h.backend.IsHotspotActive()
}
