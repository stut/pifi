package pifi

import (
	"errors"
	"fmt"
	"github.com/stut/pifi/captiveportal"
	"github.com/stut/pifi/hotspot"
	"gopkg.in/yaml.v3"
	"io"
	"log/slog"
	"os"
)

const defaultConfigFile = "/etc/pifi.yaml"

// NewFromConfig creates a new PiFi instance from the given configuration file
func NewFromConfig(filename string) (*PiFi, error) {
	pifi := &PiFi{
		Debug:         false,
		JsonLog:       false,
		CaptivePortal: captiveportal.NewCaptivePortal(),
		Hotspot:       hotspot.NewHotspot(),
	}

	if filename == "" {
		if _, err := os.Stat(defaultConfigFile); err == nil {
			filename = defaultConfigFile
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("checking for default config file: %w", err)
		} else {
			slog.Warn("No config file found, using default values")
		}
	}

	if filename != "" {
		f, err := os.Open(filename)
		if err != nil {
			return nil, fmt.Errorf("opening config file: %w", err)
		}

		defer f.Close()

		// An empty (or comment-only) file has no YAML document, which the
		// decoder reports as io.EOF. That just means "use the defaults".
		if err := yaml.NewDecoder(f).Decode(pifi); err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("parsing config file: %w", err)
		}
	}

	// A section present but null in the YAML ("hotspot:") overwrites the
	// default with a nil pointer; put the defaults back rather than running
	// with zero values.
	if pifi.Hotspot == nil {
		pifi.Hotspot = hotspot.NewHotspot()
	}
	if pifi.CaptivePortal == nil {
		pifi.CaptivePortal = captiveportal.NewCaptivePortal()
	}

	return pifi, nil
}
