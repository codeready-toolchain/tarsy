package mcp

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"text/template"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/codeready-toolchain/tarsy/pkg/config"
)

// createTransport creates an MCP SDK transport from config.
// sessionVars supplies per-execution template values (SESSION_ID = agent
// execution ID) used to resolve custom_headers. May be nil or empty for
// health/startup clients (blank X-Session-ID is omitted).
func createTransport(cfg config.TransportConfig, sessionVars map[string]string) (mcpsdk.Transport, error) {
	switch cfg.Type {
	case config.TransportTypeStdio:
		return createStdioTransport(cfg)
	case config.TransportTypeHTTP:
		return createHTTPTransport(cfg, sessionVars)
	case config.TransportTypeSSE:
		return createSSETransport(cfg, sessionVars)
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

func createHTTPTransport(cfg config.TransportConfig, sessionVars map[string]string) (*mcpsdk.StreamableClientTransport, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("HTTP transport requires url")
	}
	transport := &mcpsdk.StreamableClientTransport{
		Endpoint: cfg.URL,
	}
	if needsCustomHTTPClient(cfg) {
		httpClient, err := buildHTTPClient(cfg, sessionVars)
		if err != nil {
			return nil, err
		}
		transport.HTTPClient = httpClient
	}
	return transport, nil
}

func createSSETransport(cfg config.TransportConfig, sessionVars map[string]string) (*mcpsdk.SSEClientTransport, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("SSE transport requires url")
	}
	transport := &mcpsdk.SSEClientTransport{
		Endpoint: cfg.URL,
	}
	if needsCustomHTTPClient(cfg) {
		httpClient, err := buildHTTPClient(cfg, sessionVars)
		if err != nil {
			return nil, err
		}
		transport.HTTPClient = httpClient
	}
	return transport, nil
}

func needsCustomHTTPClient(cfg config.TransportConfig) bool {
	return cfg.BearerToken != "" || cfg.VerifySSL != nil || cfg.Timeout > 0 || len(cfg.CustomHeaders) > 0
}

// buildHTTPClient creates an http.Client with auth, TLS, timeout, and custom headers.
func buildHTTPClient(cfg config.TransportConfig, sessionVars map[string]string) (*http.Client, error) {
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

	// Restrict secret headers to the configured endpoint host so cross-origin
	// redirects do not receive Authorization / X-Session-ID / etc.
	endpointHost := ""
	if u, err := url.Parse(cfg.URL); err == nil {
		endpointHost = u.Host
	}

	// Bearer token via round-tripper wrapper
	if cfg.BearerToken != "" {
		client.Transport = &bearerTokenTransport{
			base:  client.Transport,
			token: cfg.BearerToken,
			host:  endpointHost,
		}
	}

	if len(cfg.CustomHeaders) > 0 {
		headers, err := resolveHeaderTemplates(cfg.CustomHeaders, sessionVars)
		if err != nil {
			return nil, fmt.Errorf("resolve custom_headers: %w", err)
		}
		if len(headers) > 0 {
			client.Transport = &customHeadersTransport{
				base:    client.Transport,
				headers: headers,
				host:    endpointHost,
			}
		}
	}

	// Timeout
	if cfg.Timeout > 0 {
		client.Timeout = time.Duration(cfg.Timeout) * time.Second
	}

	return client, nil
}

// resolveHeaderTemplates expands {{.VAR}} templates in header values using
// sessionVars. Empty results are omitted so health/startup clients without an
// mcp session ID do not send blank headers (e.g. X-Session-ID: "").
func resolveHeaderTemplates(headers map[string]string, sessionVars map[string]string) (map[string]string, error) {
	if len(headers) == 0 {
		return nil, nil
	}
	vars := sessionVars
	if vars == nil {
		vars = map[string]string{}
	}

	resolved := make(map[string]string, len(headers))
	for name, value := range headers {
		if !strings.Contains(value, "{{") {
			if value != "" {
				resolved[name] = value
			}
			continue
		}
		tmpl, err := template.New("header").Option("missingkey=zero").Parse(value)
		if err != nil {
			return nil, fmt.Errorf("header %q: %w", name, err)
		}
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, vars); err != nil {
			return nil, fmt.Errorf("header %q: %w", name, err)
		}
		if out := buf.String(); out != "" {
			resolved[name] = out
		}
	}
	return resolved, nil
}

// bearerTokenTransport wraps an http.RoundTripper to add Authorization headers.
// When host is set, the token is only attached to requests for that host so
// cross-origin redirects cannot receive the credential.
type bearerTokenTransport struct {
	base  http.RoundTripper
	token string
	host  string
}

func (t *bearerTokenTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	if t.host == "" || req.URL.Host == t.host {
		req.Header.Set("Authorization", "Bearer "+t.token)
	}
	return t.base.RoundTrip(req)
}

// customHeadersTransport wraps an http.RoundTripper to add resolved custom headers.
// When host is set, headers are only attached to requests for that host so
// cross-origin redirects cannot receive session/secret headers.
type customHeadersTransport struct {
	base    http.RoundTripper
	headers map[string]string
	host    string
}

func (t *customHeadersTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	if t.host == "" || req.URL.Host == t.host {
		for k, v := range t.headers {
			req.Header.Set(k, v)
		}
	}
	return t.base.RoundTrip(req)
}
