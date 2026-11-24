package netbackend

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stut/pifi/utils/shelltest"
)

// The scan loop would otherwise sleep for 30 seconds per hidden-network test.
func init() {
	nmScanAttempts = 2
	nmScanInterval = 0
}

// Hostile values exercise every bash metacharacter that matters: a quote to
// break out of quoting, command substitution, a command separator, backticks.
const (
	hostileIface    = "wlan0; reboot"
	hostileSSID     = "a'b$(reboot);`id`"
	hostilePassword = "p'w$(halt);`id`"

	quotedIface    = "'wlan0; reboot'"
	quotedSSID     = "'a'\\''b$(reboot);`id`'"
	quotedPassword = "'p'\\''w$(halt);`id`'"
)

func useTempDnsmasqDir(t *testing.T) string {
	t.Helper()
	orig := nmDnsmasqSharedDir
	nmDnsmasqSharedDir = t.TempDir()
	t.Cleanup(func() { nmDnsmasqSharedDir = orig })
	return filepath.Join(nmDnsmasqSharedDir, nmDnsmasqConfFile)
}

func TestWPA2PSKMatchesIEEETestVector(t *testing.T) {
	// IEEE 802.11i-2004 H.4.3, test case 1.
	psk, err := wpa2PSK("IEEE", "password")
	require.NoError(t, err)
	require.Equal(t, "f42c6fc52df0ebef9ebb4b90b38a5f902e83fe1b135a70e23aed762e9710a12e", psk)
}

func TestNMConfigureWifiQuotesEveryValue(t *testing.T) {
	// The passphrase never reaches the command line, only the derived key.
	hostilePSK, err := wpa2PSK(hostileSSID, hostilePassword)
	require.NoError(t, err)

	shell := shelltest.New()
	shell.Expect("wifi list", shelltest.Response{Stdout: "neighbour\n" + hostileSSID + "\n"})
	b := NewNMBackend(shell, HotspotConfig{WirelessInterface: hostileIface})

	require.NoError(t, b.ConfigureWifi(hostileSSID, hostilePassword))
	require.Equal(t, []string{
		"nmcli device wifi rescan ifname " + quotedIface,
		"nmcli -t -f SSID device wifi list ifname " + quotedIface,
		"nmcli connection delete pifi-wifi-new",
		"nmcli connection add type wifi ifname " + quotedIface +
			" con-name pifi-wifi-new autoconnect yes wifi.ssid " + quotedSSID +
			" wifi-sec.key-mgmt wpa-psk wifi-sec.psk " + hostilePSK + " wifi-sec.psk-flags 0",
		"nmcli connection up pifi-wifi-new",
		// Only once the new credentials work is the old profile replaced.
		"nmcli connection delete pifi-wifi",
		"nmcli connection modify pifi-wifi-new connection.id pifi-wifi",
	}, shell.Commands())
}

func TestNMStartHotspotQuotesAndKeepsWifiProfile(t *testing.T) {
	confPath := useTempDnsmasqDir(t)
	shell := shelltest.New()
	b := NewNMBackend(shell, HotspotConfig{
		SSID: hostileSSID, Password: hostilePassword, Channel: 6, WirelessInterface: hostileIface,
	})

	require.NoError(t, b.StartHotspot())

	// No "delete pifi-wifi": the saved profile must survive a hotspot start.
	require.Equal(t, []string{
		"nmcli connection delete pifi-hotspot",
		"nmcli connection add type wifi ifname " + quotedIface +
			" con-name pifi-hotspot autoconnect no wifi.mode ap wifi.band bg wifi.channel 6 wifi.ssid " + quotedSSID +
			" ipv4.method shared ipv4.addresses 10.42.0.1/24" +
			" wifi-security.key-mgmt wpa-psk wifi-security.psk " + quotedPassword,
		"nmcli connection up pifi-hotspot",
	}, shell.Commands())
	require.FileExists(t, confPath)
}

func TestNMConfigureWifiCreateFailureHidesPasswordAndDeletesProfile(t *testing.T) {
	var logs bytes.Buffer
	orig := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(orig) })

	shell := shelltest.New()
	shell.Expect("wifi list", shelltest.Response{Stdout: "home\n"})
	shell.Expect("connection add", shelltest.Response{
		ExitCode: 2,
		Stderr:   "Error: invalid setting\n",
		// The real shell embeds the whole command line in its error.
		Err: errors.New("cmd.Wait [nmcli connection add ... wifi-sec.psk 'hunter2-secret']: exit status 2"),
	})
	b := NewNMBackend(shell, HotspotConfig{WirelessInterface: "wlan0"})

	err := b.ConfigureWifi("home", "hunter2-secret")

	require.EqualError(t, err, "creating WiFi connection: exit code 2: Error: invalid setting")
	require.NotContains(t, logs.String(), "hunter2-secret")
	cmds := shell.Commands()
	require.Equal(t, "nmcli connection delete pifi-wifi-new", cmds[len(cmds)-1])
	require.NotContains(t, cmds, "nmcli connection delete pifi-wifi")
}

func TestNMConfigureWifiActivationFailureDeletesProfile(t *testing.T) {
	shell := shelltest.New()
	shell.Expect("wifi list", shelltest.Response{Stdout: "home\n"})
	shell.Expect("connection up pifi-wifi", shelltest.Response{ExitCode: 4, Err: errors.New("secrets were required")})
	b := NewNMBackend(shell, HotspotConfig{WirelessInterface: "wlan0"})

	err := b.ConfigureWifi("home", "wrong-password")

	require.Error(t, err)
	cmds := shell.Commands()
	require.Equal(t, []string{"nmcli connection up pifi-wifi-new", "nmcli connection delete pifi-wifi-new"}, cmds[len(cmds)-2:])
	// The saved profile that worked before must survive a bad submission.
	require.NotContains(t, cmds, "nmcli connection delete pifi-wifi")
}

func TestNMConfigureWifiUnseenSSIDIsTriedAsHidden(t *testing.T) {
	shell := shelltest.New()
	b := NewNMBackend(shell, HotspotConfig{WirelessInterface: "wlan0"})

	err := b.ConfigureWifi("secret-net", "")

	require.NoError(t, err)
	require.Contains(t, shell.Commands(),
		"nmcli connection add type wifi ifname 'wlan0' con-name pifi-wifi-new autoconnect yes wifi.ssid 'secret-net' wifi.hidden yes")
}

func TestNMWaitForSSIDUnescapesTerseOutput(t *testing.T) {
	// nmcli prints `my:net\work ` as `my\:net\\work `; the trailing space is
	// part of the SSID and CRLF must not defeat the match.
	const scan = "my\\:net\\\\work\nmy\\:net\\\\work \r\n"

	shell := shelltest.New()
	shell.Expect("wifi list", shelltest.Response{Stdout: scan})
	b := NewNMBackend(shell, HotspotConfig{WirelessInterface: "wlan0"})
	require.True(t, b.waitForSSID(`my:net\work `))
	require.Len(t, shell.Commands(), 2, "found on the first scan")

	// Without the trailing space it is a different network.
	shell = shelltest.New()
	shell.Expect("wifi list", shelltest.Response{Stdout: "my\\:net\\\\work \n"})
	b = NewNMBackend(shell, HotspotConfig{WirelessInterface: "wlan0"})
	require.False(t, b.waitForSSID(`my:net\work`))
}

func TestNMRestoreConfigFilesRemovesStaleDnsmasqDropIn(t *testing.T) {
	confPath := useTempDnsmasqDir(t)
	require.NoError(t, os.WriteFile(confPath, []byte("address=/#/10.42.0.1\n"), 0644))
	shell := shelltest.New()
	b := NewNMBackend(shell, HotspotConfig{})

	require.NoError(t, b.RestoreConfigFiles())
	require.NoFileExists(t, confPath)
	require.NoError(t, b.RestoreConfigFiles(), "nothing to remove is not an error")
	require.Empty(t, shell.Commands())
}

func TestNMStartHotspotFailureHidesPasswordAndCleansUp(t *testing.T) {
	var logs bytes.Buffer
	orig := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(orig) })

	confPath := useTempDnsmasqDir(t)
	shell := shelltest.New()
	shell.Expect("connection add", shelltest.Response{
		ExitCode: 2,
		Stderr:   "Error: invalid setting\n",
		Err:      errors.New("cmd.Wait [nmcli connection add ... wifi-security.psk 'hotspot-secret']: exit status 2"),
	})
	b := NewNMBackend(shell, HotspotConfig{SSID: "PiFi", Password: "hotspot-secret", WirelessInterface: "wlan0"})

	err := b.StartHotspot()

	require.EqualError(t, err, "creating hotspot connection: exit code 2: Error: invalid setting")
	require.NotContains(t, logs.String(), "hotspot-secret")
	require.NoFileExists(t, confPath)
}

func TestDetectBackendRequiresNetworkManager(t *testing.T) {
	shell := shelltest.New()
	shell.Expect("is-active NetworkManager", shelltest.Response{ExitCode: 3})

	_, err := DetectBackend(shell, HotspotConfig{})

	require.ErrorContains(t, err, "NetworkManager is not running")
}
