package pifi

import (
	"context"
	"fmt"
	"github.com/stut/pifi/captiveportal"
	"github.com/stut/pifi/hotspot"
	"github.com/stut/pifi/utils"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"
)

type State string

type PiFi struct {
	// Debug is whether to enable debug logging
	Debug bool `yaml:"debug" default:"false"`
	// JsonLog is whether to log in JSON format
	JsonLog bool `yaml:"jsonlog" default:"false"`
	// CaptivePortal is the captive portal web interface
	CaptivePortal *captiveportal.CaptivePortal `yaml:"captiveportal"`
	// Hotspot is the hotspot configuration
	Hotspot *hotspot.Hotspot `yaml:"hotspot"`

	// supervisor owns the periodic-check / reconnect-probe loop. Built in New.
	supervisor *hotspot.Supervisor `yaml:"-"`
}

// New creates a new PiFi instance from the given configuration file
func New(configFilename string) (*PiFi, error) {
	pifi, err := NewFromConfig(configFilename)
	if err != nil {
		return nil, fmt.Errorf("loading config file: %w", err)
	}

	pifi.configureLogging()

	err = pifi.Hotspot.Init(utils.NewBashShell())
	if err != nil {
		return nil, fmt.Errorf("Failed to initialise hotspot: %w", err)
	}

	err = pifi.CaptivePortal.Init(pifi.Hotspot.ConfigureWifi, func() (bool, error) {
		return pifi.Hotspot.ConnectivityCheck.Check(pifi.Hotspot.SSID, pifi.Hotspot.Password)
	})
	if err != nil {
		return nil, fmt.Errorf("Failed to initialise captive portal: %w", err)
	}

	pifi.supervisor = hotspot.NewSupervisor(pifi.Hotspot.ConnectivityCheckInterval)
	pifi.supervisor.Controller = pifi.Hotspot
	pifi.supervisor.Connectivity = pifi.Hotspot.ConnectivityCheck
	pifi.supervisor.Shell = utils.NewBashShell()
	pifi.supervisor.SSID = pifi.Hotspot.SSID
	pifi.supervisor.Password = pifi.Hotspot.Password
	pifi.supervisor.PortalLastActivity = pifi.CaptivePortal.LastActivity

	return pifi, nil
}

// configureLogging configures the logging
func (p *PiFi) configureLogging() {
	slogLevel := slog.LevelInfo
	if p.Debug {
		slogLevel = slog.LevelDebug
	}
	var handler slog.Handler
	if p.JsonLog {
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			AddSource: p.Debug,
			Level:     slogLevel,
		})
	} else {
		handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			AddSource: p.Debug,
			Level:     slogLevel,
		})
	}
	slog.SetDefault(slog.New(handler))
}

// Run starts the PiFi instance
func (p *PiFi) Run(ctx context.Context) error {
	var cancel context.CancelFunc
	// SIGTERM is what systemd sends on "systemctl stop"; without it the
	// hotspot would be left running after pifi exits.
	ctx, cancel = signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	p.Hotspot.SetListener(p.CaptivePortal)

	go p.supervisor.Run()
	defer func() {
		if err := p.supervisor.Shutdown(time.Minute); err != nil {
			slog.Error("Failed to shut down hotspot supervisor", "error", err)
		}
	}()

	<-ctx.Done()

	slog.Info("PiFi shutting down")
	return nil
}
