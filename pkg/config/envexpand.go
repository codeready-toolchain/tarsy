package config

import "os"

// ExpandEnv expands environment variables in YAML content using Go's standard library.
// Supports both ${VAR} and $VAR syntax (standard shell-style).
//
// Examples:
//   - ${GOOGLE_API_KEY} → value of GOOGLE_API_KEY environment variable
//   - $KUBECONFIG → value of KUBECONFIG environment variable
//   - ${DB_HOST}:${DB_PORT} → hostname:port with both variables expanded
//
// Missing variables expand to empty string. Validation should catch required fields that are empty.
func ExpandEnv(data []byte) []byte {
	expanded := os.ExpandEnv(string(data))
	return []byte(expanded)
}
