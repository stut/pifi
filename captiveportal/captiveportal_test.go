package captiveportal

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type call struct{ ssid, password string }

// newTestPortal returns an initialised portal whose configure callback reports each call on the returned channel
// and returns configureErr, and whose connectivity check reports connected.
func newTestPortal(t *testing.T, configureErr error, connected bool) (*CaptivePortal, chan call) {
	t.Helper()
	calls := make(chan call, 4)
	cp := NewCaptivePortal()
	require.NoError(t, cp.Init(
		func(ssid, password string) error {
			calls <- call{ssid, password}
			return configureErr
		},
		func() (bool, error) { return connected, nil },
	))
	return cp, calls
}

func request(cp *CaptivePortal, method, target string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	rec := httptest.NewRecorder()
	cp.newHandler().ServeHTTP(rec, req)
	return rec
}

func TestConfigureWifiRejectsInvalidInput(t *testing.T) {
	for name, form := range map[string]url.Values{
		"empty ssid":      {"ssid": {""}, "password": {"longenough"}},
		"long ssid":       {"ssid": {strings.Repeat("a", 33)}},
		"short password":  {"ssid": {"home"}, "password": {"short"}},
		"oversized body":  {"ssid": {"home"}, "password": {"longenough"}, "junk": {strings.Repeat("a", 10000)}},
		"control in ssid": {"ssid": {"home\n$(reboot)"}, "password": {"longenough"}},
	} {
		t.Run(name, func(t *testing.T) {
			cp, calls := newTestPortal(t, nil, false)
			rec := request(cp, http.MethodPost, "/configure-wifi", form)
			require.Equal(t, http.StatusOK, rec.Code)
			require.Contains(t, rec.Body.String(), `class="error visible"`)
			require.Empty(t, calls, "callback must not run for invalid input")
		})
	}
}

func TestConfigureWifiValidInput(t *testing.T) {
	cp, calls := newTestPortal(t, nil, false)
	rec := request(cp, http.MethodPost, "/configure-wifi", url.Values{"ssid": {"it's home"}, "password": {"s3cret pass'word"}})
	require.Equal(t, http.StatusFound, rec.Code)
	require.Equal(t, "/connecting?skip-check=true", rec.Header().Get("Location"))

	select {
	case got := <-calls:
		require.Equal(t, call{"it's home", "s3cret pass'word"}, got)
	case <-time.After(2 * time.Second):
		t.Fatal("callback was not invoked")
	}

	// The SSID is offered back, the password never is.
	body := request(cp, http.MethodGet, "/configure-wifi", nil).Body.String()
	require.Contains(t, body, "it&#39;s home")
	require.NotContains(t, body, "s3cret")
}

func TestConfigureWifiFailureIsShownOnForm(t *testing.T) {
	cp, calls := newTestPortal(t, errors.New("nmcli ... password s3cretpassword"), false)
	form := url.Values{"ssid": {"home"}, "password": {"s3cretpassword"}}
	require.Equal(t, http.StatusFound, request(cp, http.MethodPost, "/configure-wifi", form).Code)
	<-calls

	var body string
	require.Eventually(t, func() bool {
		body = request(cp, http.MethodGet, "/configure-wifi", nil).Body.String()
		return strings.Contains(body, "Could not connect to home")
	}, 2*time.Second, time.Millisecond)
	require.NotContains(t, body, "s3cretpassword")

	// Once the attempt has failed the spinner page sends the user back to the form.
	rec := request(cp, http.MethodGet, "/connecting", nil)
	require.Equal(t, "/configure-wifi", rec.Header().Get("Location"))

	// A new attempt clears the previous failure straight away.
	cp.configureWifiCallback = func(string, string) error { return nil }
	request(cp, http.MethodPost, "/configure-wifi", form)
	require.NotContains(t, request(cp, http.MethodGet, "/configure-wifi", nil).Body.String(), "Could not connect")
}

func TestConnecting(t *testing.T) {
	cp, _ := newTestPortal(t, nil, true)
	rec := request(cp, http.MethodGet, "/connecting", nil)
	require.Equal(t, http.StatusFound, rec.Code)
	require.Equal(t, cp.RedirectUrl, rec.Header().Get("Location"))
	require.NotContains(t, rec.Body.String(), "spinner", "nothing may be rendered after the redirect")

	// The check is skipped straight after submitting, while the hotspot is still coming down.
	rec = request(cp, http.MethodGet, "/connecting?skip-check=true", nil)
	require.Equal(t, http.StatusOK, rec.Code)

	cp, _ = newTestPortal(t, nil, false)
	rec = request(cp, http.MethodGet, "/connecting", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "Connecting to WiFi")
	require.Contains(t, rec.Body.String(), `window.location.href = "/connecting"`)
}

func TestInitReportsTemplateProblems(t *testing.T) {
	noop := func(string, string) error { return nil }
	check := func() (bool, error) { return false, nil }

	cp := NewCaptivePortal()
	cp.TemplatesDir = filepath.Join(t.TempDir(), "missing")
	require.Error(t, cp.Init(noop, check))

	dir := t.TempDir()
	require.NoError(t, os.CopyFS(dir, os.DirFS("templates")))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "connecting.html"), []byte("{{ if }}"), 0o600))
	cp.TemplatesDir = dir
	cp.CacheTemplates = false
	require.Error(t, cp.Init(noop, check))
}

func TestUncachedTemplatesAreReadFromTemplatesDir(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.CopyFS(dir, os.DirFS("templates")))

	cp := NewCaptivePortal()
	cp.TemplatesDir = dir
	cp.CacheTemplates = false
	require.NoError(t, cp.Init(func(string, string) error { return nil }, func() (bool, error) { return false, nil }))

	layout := filepath.Join(dir, "layout.html")
	require.NoError(t, os.WriteFile(layout, []byte(`edited on disk {{block "content" .}}{{end}}`), 0o600))
	require.Contains(t, request(cp, http.MethodGet, "/configure-wifi", nil).Body.String(), "edited on disk")

	// A template that breaks after startup is an error response, not a crash.
	require.NoError(t, os.WriteFile(layout, []byte("{{ if }}"), 0o600))
	require.Equal(t, http.StatusInternalServerError, request(cp, http.MethodGet, "/configure-wifi", nil).Code)
}

func TestStartFailsWhenPortTaken(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer taken.Close()

	cp, _ := newTestPortal(t, nil, false)
	cp.ListenAddress = taken.Addr().String()
	require.Error(t, cp.Start())
	require.NoError(t, cp.Stop())
}

func TestStartStopStart(t *testing.T) {
	// Reserve a free port and release it, so both starts bind the same address and the second one proves Stop
	// let go of it.
	free, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := free.Addr().String()
	require.NoError(t, free.Close())

	cp, _ := newTestPortal(t, nil, false)
	cp.ListenAddress = addr
	require.NoError(t, cp.Stop(), "Stop before Start")

	for range 2 {
		require.NoError(t, cp.Start())
		resp, err := http.Get("http://" + addr + "/configure-wifi")
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Contains(t, string(body), `name="ssid"`)
		require.NoError(t, cp.Stop())
		require.NoError(t, cp.Stop(), "Stop twice")
	}
}

func TestCrossOriginWifiSubmissionIsRejected(t *testing.T) {
	for _, header := range []string{"Origin", "Sec-Fetch-Site"} {
		t.Run(header, func(t *testing.T) {
			cp, calls := newTestPortal(t, nil, false)
			form := url.Values{"ssid": {"home"}, "password": {"longenough"}}
			req := httptest.NewRequest(http.MethodPost, "http://10.42.0.1/configure-wifi", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if header == "Origin" {
				req.Header.Set(header, "http://untrusted.example")
			} else {
				req.Header.Set(header, "cross-site")
			}
			rec := httptest.NewRecorder()
			cp.newHandler().ServeHTTP(rec, req)
			require.Equal(t, http.StatusForbidden, rec.Code)
			require.Empty(t, calls)
			require.True(t, cp.LastActivity().IsZero())
		})
	}
}

func TestSameOriginWifiSubmissionIsAccepted(t *testing.T) {
	cp, calls := newTestPortal(t, nil, false)
	req := httptest.NewRequest(http.MethodPost, "http://10.42.0.1/configure-wifi", strings.NewReader("ssid=home&password=longenough"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://10.42.0.1")
	rec := httptest.NewRecorder()
	cp.newHandler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusFound, rec.Code)
	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatal("same-origin submission did not configure WiFi")
	}
}
