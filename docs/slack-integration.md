# Slack Integration

TARSy can send automatic notifications to Slack when alert processing starts, completes, fails, times out, or is cancelled. This feature is **disabled by default** and requires configuration to enable.

## Table of Contents
- [Overview](#overview)
- [How It Works](#how-it-works)
- [Setup Instructions](#setup-instructions)
- [Configuration](#configuration)
- [Slack Notification Threading](#slack-notification-threading)
- [How to Test Locally](#how-to-test-locally)

## Overview

When configured, TARSy automatically:
- Sends a **start notification** when processing begins (only for Slack-originated alerts with a fingerprint)
- Sends a **terminal notification** when processing reaches a final state (completed, failed, timed out, cancelled)
- Includes analysis summary with a link to the detailed dashboard view
- Supports **threaded replies** to original Slack alert messages via fingerprint correlation

All notifications use Slack Block Kit for rich formatting with emoji status indicators.

**Default State**: Slack integration is **disabled by default**. TARSy will process alerts normally without sending any Slack notifications until you configure it.

## How It Works

### Standard Slack Message Notification

1. **Alert arrives** (via API or dashboard)
2. **TARSy processes** the alert through the agent chain
3. **After processing**, TARSy posts a Slack message to the configured channel with:
   - Analysis summary (completed -- green)
   - Error message (failed -- red)
   - Timeout message (timed out -- hourglass)
   - Cancelled message (cancelled -- no entry)
   - Link to full analysis in dashboard (`<dashboard-url>/sessions/<session-id>`)

**Note**: Start notifications are NOT sent for standard (non-threaded) alerts to avoid unnecessary noise.

### Threaded Slack Message Notification

1. **Alert arrives** with a `slack_message_fingerprint` (unique identifier linking it to a Slack message)
2. **TARSy starts processing** and immediately sends a start notification as a thread reply
3. **TARSy processes** the alert through the agent chain
4. **After processing**, TARSy searches the Slack channel history (last 24 hours) for the message containing the fingerprint
5. **Finds target message**, posts a threaded reply with the analysis result and dashboard link

**Note**: Start notifications are ONLY sent for alerts with a `slack_message_fingerprint`. This provides immediate feedback in the original Slack thread that processing has begun.

### Notification Status Indicators

| Status | Emoji | Label |
|--------|-------|-------|
| Started | :arrows_counterclockwise: | Processing Started |
| Completed | :white_check_mark: | Analysis Complete |
| Failed | :x: | Analysis Failed |
| Timed Out | :hourglass: | Analysis Timed Out |
| Cancelled | :no_entry_sign: | Analysis Cancelled |

Content selection for completed sessions: executive summary (preferred) -> final analysis (fallback) -> status + dashboard link only. Text blocks are truncated at 2900 characters.

## Setup Instructions

### Step 1: Create a Slack App

1. Go to the Slack Apps page: [Slack Apps](https://api.slack.com/apps)
2. Create your Slack app

### Step 2: Configure Bot Permissions

In your app's **OAuth & Permissions** page, under **Bot Token Scopes**, add these scopes:

| Scope | Purpose |
|-------|---------|
| `channels:history` | Read messages in public channels (to find original alerts for threading) |
| `groups:history` | Read messages in private channels |
| `chat:write` | Post messages and replies |
| `channels:read` | View basic channel info |
| `groups:read` | View basic private channel info |

### Step 3: Install App to Workspace

1. Under OAuth Tokens, click **"Install to Workspace"** (or **"Reinstall App"** if updating scopes)
2. Authorize the app
3. **Copy the Bot User OAuth Token** (starts with `xoxb-`)

### Step 4: Invite Bot to Channel

In your Slack channel, invite your bot: `/invite @your-bot-name`

### Step 5: Get Channel ID

Right-click on your channel -> **"View channel details"** -> Copy the Channel ID (e.g., `C12345678`)

Or find it in the channel URL: `https://your-workspace.slack.com/archives/C12345678`

## Configuration

TARSy uses a two-part configuration for Slack: YAML settings in `deploy/config/tarsy.yaml` and the bot token as an environment variable.

### 1. YAML Configuration (`deploy/config/tarsy.yaml`)

Add or update the `system.slack` section:

```yaml
system:
  # Base URL for dashboard links in Slack notifications
  dashboard_url: "https://tarsy.example.com"

  slack:
    enabled: true
    token_env: "SLACK_BOT_TOKEN"   # Env var name for the bot token (default: SLACK_BOT_TOKEN)
    channel: "C12345678"           # Slack channel ID (required when enabled)
```

### 2. Environment Variable (`.env` or deployment secrets)

Set the bot token in your environment. For local development, add it to `deploy/config/.env`:

```
SLACK_BOT_TOKEN=xoxb-your-token-here
```

For OpenShift deployments, the token is stored in the `tarsy-secrets` Kubernetes Secret.

### Configuration Fields

| Field | Required | Default | Description |
|-------|----------|---------|-------------|
| `system.slack.enabled` | No | `false` | Enable/disable Slack notifications |
| `system.slack.token_env` | No | `SLACK_BOT_TOKEN` | Name of the environment variable containing the bot token |
| `system.slack.channel` | Yes (when enabled) | -- | Slack channel ID to post notifications to |
| `system.dashboard_url` | No | `http://localhost:5173` | Base URL for dashboard links in notification messages |

### Validation

At startup, TARSy validates the Slack configuration:
- `channel` must be set when `enabled: true`
- The environment variable specified by `token_env` must contain a value
- Validation failure prevents startup (fail-hard) to avoid running with a broken Slack setup

When Slack is not configured (`enabled: false` or missing token/channel), `slack.NewService` returns nil. All methods are nil-receiver safe, so no nil checks are needed in calling code.

## Slack Notification Threading

To enable threaded replies, include a `slack_message_fingerprint` when submitting an alert to TARSy.

### Fingerprint Requirements

The fingerprint matching is **flexible and forgiving**:

- **Case-insensitive**: `"Fingerprint: 123"`, `"fingerprint: 123"`, and `"FINGERPRINT: 123"` all match
- **Whitespace-normalized**: Extra spaces, newlines, and tabs are ignored
- **Position-independent**: The fingerprint can appear anywhere in the message text or attachments

**Examples of valid fingerprint placements in Slack messages:**

```
# Beginning of message
Fingerprint: alert-123
Alert: Pod CrashLooping in namespace prod

# Middle of message
Alert: High CPU usage
Fingerprint: alert-456
Environment: production

# End of message
Alert: Database connection timeout
Environment: staging
Fingerprint: alert-789
```

### How TARSy Finds Your Message

1. Searches the last **24 hours** of channel history (up to 50 messages)
2. Checks message text and all attachment fields (`text` and `fallback`)
3. Uses case-insensitive matching with whitespace normalization
4. Returns the first message that contains the fingerprint
5. If not found, posts to the channel directly (no error)

### ThreadTS Caching

When an alert has a fingerprint, the start notification resolves the target message's `thread_ts` and caches it. The terminal notification reuses this cached value, avoiding a redundant `conversations.history` API call.

## How to Test Locally

### Standard Slack Message Notification

1. Follow the [Setup Instructions](#setup-instructions)
2. Configure Slack in `deploy/config/tarsy.yaml` (set `enabled: true` and your channel ID)
3. Set `SLACK_BOT_TOKEN` in your `deploy/config/.env` file
4. Start TARSy (`make dev` or `make containers-deploy`)
5. Submit an alert via the dashboard at `/submit-alert`
6. Check the TARSy notification in your Slack channel

### Threaded Slack Message Notification

1. Follow the [Setup Instructions](#setup-instructions) and [Configuration](#configuration)
2. Start TARSy
3. Post a message containing a fingerprint to your Slack channel:

```bash
curl -X POST https://slack.com/api/chat.postMessage \
  -H "Authorization: Bearer $SLACK_BOT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "channel": "C12345678",
    "text": "Fingerprint: test-121212\nMessage: Namespace test terminating"
  }'
```

**Note**: The fingerprint format is flexible -- case and whitespace do not matter. All these work:
- `"Fingerprint: 121212"`
- `"fingerprint: 121212"`
- `"FINGERPRINT:121212"`

4. Submit an alert via the dashboard. Include the same fingerprint value in the `slack_message_fingerprint` field.
5. Check the TARSy notification as a thread reply on your original Slack message.

### Start Notifications

When an alert with a `slack_message_fingerprint` arrives:
1. TARSy immediately sends a start notification to the Slack thread
2. The message includes:
   - Start indicator showing processing has begun
   - Link to session details for real-time monitoring

**Key behaviors:**
- **Only sent for Slack-originated alerts** (those with a `slack_message_fingerprint`)
- **Not sent for standard alerts** (submitted without a fingerprint)
- Provides immediate feedback in the Slack thread
- Allows users to track the processing lifecycle: START -> COMPLETE/FAIL/TIMEOUT
