package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	pifiImpl "github.com/stut/pifi/pifi"
	"github.com/stut/pifi/setup"
	"github.com/stut/pifi/utils"
)

const version = "0.1.0"

const defaultWirelessInterface = "wlan0"

func main() {
	showVersion := flag.Bool("v", false, "Print the version and exit")
	configFilename := flag.String("c", "", "Path to the configuration file")
	flag.Parse()

	if *showVersion {
		fmt.Printf("PiFi v%s\n", version)
		return
	}

	// Handle "setup" subcommand
	if flag.NArg() > 0 && flag.Arg(0) == "setup" {
		if err := setup.Run(utils.NewBashShell(), defaultWirelessInterface); err != nil {
			fmt.Fprintf(os.Stderr, "Setup failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Pre-flight failures exit non-zero so systemd sees the unit as failed.
	if msg := setup.CheckRoot(); msg != "" {
		fmt.Fprintln(os.Stderr, msg)
		os.Exit(1)
	}

	// Check if setup has been run (config file exists)
	configExists := configFileExists(*configFilename)
	if !configExists {
		if setup.IsInteractive() {
			fmt.Fprintln(os.Stderr, "PiFi has not been set up yet. Run 'sudo pifi setup' to configure it.")
		} else {
			slog.Error("PiFi is not configured. Run 'sudo pifi setup' to set up.")
		}
		os.Exit(1)
	}

	// The interface is configurable, so it can only be checked once the
	// config has been loaded.
	config, err := pifiImpl.NewFromConfig(*configFilename)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Failed to load config file:", err)
		os.Exit(1)
	}
	if msg := setup.CheckWirelessInterface(config.Hotspot.WirelessInterface); msg != "" {
		fmt.Fprintln(os.Stderr, msg)
		os.Exit(1)
	}

	pifi, err := pifiImpl.New(*configFilename)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Failed to start PiFi:", err)
		os.Exit(1)
	}

	if err := pifi.Run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "PiFi failed:", err)
		os.Exit(1)
	}
}

func configFileExists(explicitPath string) bool {
	if explicitPath != "" {
		_, err := os.Stat(explicitPath)
		return err == nil
	}
	_, err := os.Stat("/etc/pifi.yaml")
	return err == nil
}
