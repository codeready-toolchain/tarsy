package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	echo "github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
)

func TestExtractAuthor(t *testing.T) {
	tests := []struct {
		name     string
		headers  map[string]string
		expected string
	}{
		{
			name:     "no headers returns default",
			headers:  map[string]string{},
			expected: "api-client",
		},
		{
			name: "X-Forwarded-User takes priority",
			headers: map[string]string{
				"X-Forwarded-User":  "alice",
				"X-Forwarded-Email": "alice@example.com",
			},
			expected: "alice",
		},
		{
			name: "X-Forwarded-Email used when no user",
			headers: map[string]string{
				"X-Forwarded-Email": "bob@example.com",
			},
			expected: "bob@example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			result := extractAuthor(c)
			assert.Equal(t, tt.expected, result)
		})
	}
}
