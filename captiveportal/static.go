package captiveportal

import (
	"embed"
	_ "embed"
	"net/http"
)

//go:embed static
var embeddedStatic embed.FS

// newHandler returns a new http.Handler serving static files for the captive portal
func (c *CaptivePortal) newStaticHandler() http.Handler {
	if c.StaticDir != "" {
		return http.StripPrefix("/static", http.FileServer(http.Dir(c.StaticDir)))
	}

	return http.FileServer(http.FS(embeddedStatic))
}
