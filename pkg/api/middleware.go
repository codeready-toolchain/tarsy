package api

import (
	"net/http"
	"strconv"
	"time"

	echo "github.com/labstack/echo/v5"

	"github.com/codeready-toolchain/tarsy/pkg/metrics"
)

// securityHeaders returns middleware that sets standard security response headers.
func securityHeaders() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			h := c.Response().Header()
			h.Set("X-Frame-Options", "DENY")
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
			return next(c)
		}
	}
}

// prometheusMiddleware records HTTP request count and duration for all routes
// except /health and /metrics (which would create scrape noise).
func prometheusMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			path := c.RouteInfo().Path
			if path == "/health" || path == "/metrics" {
				return next(c)
			}

			sw := &statusWriter{ResponseWriter: c.Response(), code: http.StatusOK}
			c.SetResponse(sw)

			start := time.Now()
			err := next(c)
			duration := time.Since(start)

			method := c.Request().Method
			metrics.HTTPRequestsTotal.WithLabelValues(method, path, strconv.Itoa(sw.code)).Inc()
			metrics.HTTPDurationSeconds.WithLabelValues(method, path).Observe(duration.Seconds())
			return err
		}
	}
}

// statusWriter wraps http.ResponseWriter to capture the status code.
type statusWriter struct {
	http.ResponseWriter
	code    int
	written bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.written {
		w.code = code
		w.written = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.written {
		w.written = true
	}
	return w.ResponseWriter.Write(b)
}
