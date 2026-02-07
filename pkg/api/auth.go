package api

import (
	echo "github.com/labstack/echo/v5"
)

// extractAuthor extracts the author from oauth2-proxy headers.
// Priority: X-Forwarded-User > X-Forwarded-Email > "api-client"
func extractAuthor(c *echo.Context) string {
	if user := c.Request().Header.Get("X-Forwarded-User"); user != "" {
		return user
	}
	if email := c.Request().Header.Get("X-Forwarded-Email"); email != "" {
		return email
	}
	return "api-client"
}
