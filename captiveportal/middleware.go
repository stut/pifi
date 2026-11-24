package captiveportal

import (
	"log/slog"
	"net/http"
	"time"
)

// loggingMiddleware provides a simple log of HTTP requests and tracks activity
func (c *CaptivePortal) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		c.RecordActivity()
		startTime := time.Now()

		next.ServeHTTP(writer, req)

		elapsedTime := time.Since(startTime)
		slog.Debug("HTTP request",
			"method", req.Method,
			"url", req.URL.Path,
			"duration", elapsedTime)
	})
}
