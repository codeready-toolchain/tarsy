package slack

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	goslack "github.com/slack-go/slack"
)

// Client is a thin wrapper around the slack-go SDK.
type Client struct {
	api       *goslack.Client
	channelID string
	logger    *slog.Logger
}

// NewClient creates a new Slack API client.
func NewClient(token, channelID string) *Client {
	return &Client{
		api:       goslack.New(token),
		channelID: channelID,
		logger:    slog.Default().With("component", "slack-client"),
	}
}

// NewClientWithAPIURL creates a Slack API client that targets a custom API URL.
// Useful for testing with a mock server.
func NewClientWithAPIURL(token, channelID, apiURL string) *Client {
	return &Client{
		api:       goslack.New(token, goslack.OptionAPIURL(apiURL)),
		channelID: channelID,
		logger:    slog.Default().With("component", "slack-client"),
	}
}

// PostMessage sends a message to the configured channel.
// If threadTS is non-empty, the message is posted as a threaded reply.
func (c *Client) PostMessage(ctx context.Context, blocks []goslack.Block, threadTS string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	opts := []goslack.MsgOption{
		goslack.MsgOptionBlocks(blocks...),
	}
	if threadTS != "" {
		opts = append(opts, goslack.MsgOptionTS(threadTS))
	}

	_, _, err := c.api.PostMessageContext(ctx, c.channelID, opts...)
	if err != nil {
		return fmt.Errorf("chat.postMessage failed: %w", err)
	}
	return nil
}

// FindMessageByFingerprint searches recent channel history for a message
// containing the given fingerprint text. Returns the message timestamp (ts)
// for threading, or empty string if not found.
func (c *Client) FindMessageByFingerprint(ctx context.Context, fingerprint string) (string, error) {
	oldest := fmt.Sprintf("%d", time.Now().Add(-24*time.Hour).Unix())

	params := &goslack.GetConversationHistoryParameters{
		ChannelID: c.channelID,
		Oldest:    oldest,
		Limit:     50,
	}
	history, err := c.api.GetConversationHistoryContext(ctx, params)
	if err != nil {
		return "", fmt.Errorf("conversations.history failed: %w", err)
	}

	normalizedFingerprint := normalizeText(fingerprint)
	for _, msg := range history.Messages {
		text := collectMessageText(msg)
		if strings.Contains(normalizeText(text), normalizedFingerprint) {
			return msg.Timestamp, nil
		}
	}
	return "", nil
}
