package api

import (
	echo "github.com/labstack/echo/v5"
)

// extractAuthor extracts the author from proxy headers.
// Priority: X-Forwarded-User (oauth2-proxy) > X-Forwarded-Email (oauth2-proxy) >
// X-Remote-User (kube-rbac-proxy) > "api-client"
func extractAuthor(c *echo.Context) string {
	if user := c.Request().Header.Get("X-Forwarded-User"); user != "" {
		return user
	}
	if email := c.Request().Header.Get("X-Forwarded-Email"); email != "" {
		return email
	}
	if user := c.Request().Header.Get("X-Remote-User"); user != "" {
		return user
	}
	return "api-client"
}
