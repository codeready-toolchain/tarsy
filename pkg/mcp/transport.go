package mcp

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/codeready-toolchain/tarsy/pkg/config"
)

// createTransport creates an MCP SDK transport from config.
func createTransport(cfg config.TransportConfig) (mcpsdk.Transport, error) {
	switch cfg.Type {
	case config.TransportTypeStdio:
		return createStdioTransport(cfg)
	case config.TransportTypeHTTP:
		return createHTTPTransport(cfg)
	case config.TransportTypeSSE:
		return createSSETransport(cfg)
	default:
		return nil, fmt.Errorf("unsupported transport type: %s", cfg.Type)
	}
}

func createStdioTransport(cfg config.TransportConfig) (*mcpsdk.CommandTransport, error) {
	if cfg.Command == "" {
		return nil, fmt.Errorf("stdio transport requires command")
	}

	cmd := exec.Command(cfg.Command, cfg.Args...)

	// Inherit parent environment + config overrides.
	// Template vars (e.g., {{.KUBECONFIG}}) are already resolved by the config loader.
	env := os.Environ()
	for k, v := range cfg.Env {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	cmd.Env = env

	return &mcpsdk.CommandTransport{Command: cmd}, nil
}

func createHTTPTransport(cfg config.TransportConfig) (*mcpsdk.StreamableClientTransport, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("HTTP transport requires url")
	}
	transport := &mcpsdk.StreamableClientTransport{
		Endpoint: cfg.URL,
	}
	if cfg.BearerToken != "" || cfg.VerifySSL != nil || cfg.Timeout > 0 {
		transport.HTTPClient = buildHTTPClient(cfg)
	}
	return transport, nil
}

func createSSETransport(cfg config.TransportConfig) (*mcpsdk.SSEClientTransport, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("SSE transport requires url")
	}
	transport := &mcpsdk.SSEClientTransport{
		Endpoint: cfg.URL,
	}
	if cfg.BearerToken != "" || cfg.VerifySSL != nil || cfg.Timeout > 0 {
		transport.HTTPClient = buildHTTPClient(cfg)
	}
	return transport, nil
}

// buildHTTPClient creates an http.Client with auth, TLS, and timeout settings.
func buildHTTPClient(cfg config.TransportConfig) *http.Client {
	httpTransport := http.DefaultTransport.(*http.Transport).Clone()

	// TLS verification
	if cfg.VerifySSL != nil && !*cfg.VerifySSL {
		httpTransport.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: true,             //nolint:gosec // user-configured
			MinVersion:         tls.VersionTLS12, // prevent protocol downgrade even in relaxed mode
		}
	}

	client := &http.Client{
		Transport: httpTransport,
	}

	// Bearer token via round-tripper wrapper
	if cfg.BearerToken != "" {
		client.Transport = &bearerTokenTransport{
			base:  client.Transport,
			token: cfg.BearerToken,
		}
	}

	// Timeout
	if cfg.Timeout > 0 {
		client.Timeout = time.Duration(cfg.Timeout) * time.Second
	}

	return client
}

// bearerTokenTransport wraps an http.RoundTripper to add Authorization headers.
type bearerTokenTransport struct {
	base  http.RoundTripper
	token string
}

func (t *bearerTokenTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(req)
}
