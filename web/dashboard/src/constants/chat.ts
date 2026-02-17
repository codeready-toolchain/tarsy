/**
 * Chat-related constants.
 *
 * These values must match the backend API validation limits defined in:
 * pkg/api/handler_chat.go (SendChatMessageRequest validation)
 */

/**
 * Maximum character length for a single chat message.
 * Backend enforces this limit at the API level.
 */
export const MAX_MESSAGE_LENGTH = 100_000;

/**
 * Character count threshold at which to show warning to user.
 * Set at 90% of max length to give users advance notice.
 */
export const WARNING_THRESHOLD = 90_000;
