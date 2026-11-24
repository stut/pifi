package main

import (
	"fmt"
	"github.com/stut/pifi/captiveportal"
	"github.com/stut/pifi/hotspot/connectivitycheck"
	"github.com/stut/pifi/utils"
	"log/slog"
	"os"
)

func main() {
	slog.SetDefault(slog.New(
		slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			AddSource: true,
			Level:     slog.LevelDebug,
		})))

	// This periodically checks the connection status and writes it to a status file. If the connection is lost,
	// it starts a hotspot with the given SSID and password, and adds them to the status file.
	connectivityCheck := connectivitycheck.NewConnectivityCheck()

	// This is the captive portal that the user sees when they connect to the hotspot. It allows the user to
	// configure the wifi settings.
	cp := captiveportal.NewCaptivePortal()
	cp.ListenAddress = "127.0.0.1:8080"
	cp.StaticDir = "captiveportal/static"
	cp.TemplatesDir = "captiveportal/templates"
	cp.CacheTemplates = false

	connectivityCheck.SetShell(utils.NewBashShell())
	err := cp.Init(configureWifiCallback, func() (bool, error) {
		return connectivityCheck.Check("", "")
	})
	if err != nil {
		fmt.Println("Failed to initialise captive portal:", err)
		return
	}

	err = cp.Start()
	if err != nil {
		fmt.Println("Failed to start captive portal:", err)
		return
	}

	select {}
}

func configureWifiCallback(ssid, _ string) error {
	fmt.Printf("Configuring wifi:\n  SSID: %s\n\n", ssid)
	return nil
}
