package agent

// MaxAlertDataSize is the maximum allowed size for alert data (1 MB).
// Alerts exceeding this limit are rejected at API submission time (HTTP 413).
const MaxAlertDataSize = 1 * 1024 * 1024 // 1 MB
