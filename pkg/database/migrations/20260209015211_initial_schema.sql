-- Create "alert_sessions" table
CREATE TABLE "public"."alert_sessions" (
  "session_id" character varying NOT NULL,
  "alert_data" text NOT NULL,
  "agent_type" character varying NOT NULL,
  "alert_type" character varying NULL,
  "status" character varying NOT NULL DEFAULT 'pending',
  "created_at" timestamptz NOT NULL,
  "started_at" timestamptz NULL,
  "completed_at" timestamptz NULL,
  "error_message" character varying NULL,
  "final_analysis" text NULL,
  "executive_summary" text NULL,
  "executive_summary_error" character varying NULL,
  "session_metadata" jsonb NULL,
  "author" character varying NULL,
  "runbook_url" character varying NULL,
  "mcp_selection" jsonb NULL,
  "chain_id" character varying NOT NULL,
  "current_stage_index" bigint NULL,
  "current_stage_id" character varying NULL,
  "pod_id" character varying NULL,
  "last_interaction_at" timestamptz NULL,
  "slack_message_fingerprint" character varying NULL,
  "deleted_at" timestamptz NULL,
  PRIMARY KEY ("session_id")
);
-- Create index "alertsession_agent_type" to table: "alert_sessions"
CREATE INDEX "alertsession_agent_type" ON "public"."alert_sessions" ("agent_type");
-- Create index "alertsession_alert_type" to table: "alert_sessions"
CREATE INDEX "alertsession_alert_type" ON "public"."alert_sessions" ("alert_type");
-- Create index "alertsession_chain_id" to table: "alert_sessions"
CREATE INDEX "alertsession_chain_id" ON "public"."alert_sessions" ("chain_id");
-- Create index "alertsession_deleted_at" to table: "alert_sessions"
CREATE INDEX "alertsession_deleted_at" ON "public"."alert_sessions" ("deleted_at") WHERE (deleted_at IS NOT NULL);
-- Create index "alertsession_status" to table: "alert_sessions"
CREATE INDEX "alertsession_status" ON "public"."alert_sessions" ("status");
-- Create index "alertsession_status_created_at" to table: "alert_sessions"
CREATE INDEX "alertsession_status_created_at" ON "public"."alert_sessions" ("status", "created_at");
-- Create index "alertsession_status_last_interaction_at" to table: "alert_sessions"
CREATE INDEX "alertsession_status_last_interaction_at" ON "public"."alert_sessions" ("status", "last_interaction_at");
-- Create index "alertsession_status_started_at" to table: "alert_sessions"
CREATE INDEX "alertsession_status_started_at" ON "public"."alert_sessions" ("status", "started_at");
-- Create "chats" table
CREATE TABLE "public"."chats" (
  "chat_id" character varying NOT NULL,
  "created_at" timestamptz NOT NULL,
  "created_by" character varying NULL,
  "chain_id" character varying NOT NULL,
  "pod_id" character varying NULL,
  "last_interaction_at" timestamptz NULL,
  "session_id" character varying NOT NULL,
  PRIMARY KEY ("chat_id"),
  CONSTRAINT "chats_alert_sessions_chat" FOREIGN KEY ("session_id") REFERENCES "public"."alert_sessions" ("session_id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create index "chat_created_at" to table: "chats"
CREATE INDEX "chat_created_at" ON "public"."chats" ("created_at");
-- Create index "chat_pod_id_last_interaction_at" to table: "chats"
CREATE INDEX "chat_pod_id_last_interaction_at" ON "public"."chats" ("pod_id", "last_interaction_at");
-- Create index "chat_session_id" to table: "chats"
CREATE UNIQUE INDEX "chat_session_id" ON "public"."chats" ("session_id");
-- Create "chat_user_messages" table
CREATE TABLE "public"."chat_user_messages" (
  "message_id" character varying NOT NULL,
  "content" text NOT NULL,
  "author" character varying NOT NULL,
  "created_at" timestamptz NOT NULL,
  "chat_id" character varying NOT NULL,
  PRIMARY KEY ("message_id"),
  CONSTRAINT "chat_user_messages_chats_user_messages" FOREIGN KEY ("chat_id") REFERENCES "public"."chats" ("chat_id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create index "chatusermessage_chat_id" to table: "chat_user_messages"
CREATE INDEX "chatusermessage_chat_id" ON "public"."chat_user_messages" ("chat_id");
-- Create index "chatusermessage_created_at" to table: "chat_user_messages"
CREATE INDEX "chatusermessage_created_at" ON "public"."chat_user_messages" ("created_at");
-- Create "stages" table
CREATE TABLE "public"."stages" (
  "stage_id" character varying NOT NULL,
  "stage_name" character varying NOT NULL,
  "stage_index" bigint NOT NULL,
  "expected_agent_count" bigint NOT NULL,
  "parallel_type" character varying NULL,
  "success_policy" character varying NULL,
  "status" character varying NOT NULL DEFAULT 'pending',
  "started_at" timestamptz NULL,
  "completed_at" timestamptz NULL,
  "duration_ms" bigint NULL,
  "error_message" character varying NULL,
  "session_id" character varying NOT NULL,
  "chat_id" character varying NULL,
  "chat_user_message_id" character varying NULL,
  PRIMARY KEY ("stage_id"),
  CONSTRAINT "stages_alert_sessions_stages" FOREIGN KEY ("session_id") REFERENCES "public"."alert_sessions" ("session_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "stages_chat_user_messages_stage" FOREIGN KEY ("chat_user_message_id") REFERENCES "public"."chat_user_messages" ("message_id") ON UPDATE NO ACTION ON DELETE SET NULL,
  CONSTRAINT "stages_chats_stages" FOREIGN KEY ("chat_id") REFERENCES "public"."chats" ("chat_id") ON UPDATE NO ACTION ON DELETE SET NULL
);
-- Create index "stage_session_id_stage_index" to table: "stages"
CREATE UNIQUE INDEX "stage_session_id_stage_index" ON "public"."stages" ("session_id", "stage_index");
-- Create index "stages_chat_user_message_id_key" to table: "stages"
CREATE UNIQUE INDEX "stages_chat_user_message_id_key" ON "public"."stages" ("chat_user_message_id");
-- Create "agent_executions" table
CREATE TABLE "public"."agent_executions" (
  "execution_id" character varying NOT NULL,
  "agent_name" character varying NOT NULL,
  "agent_index" bigint NOT NULL,
  "status" character varying NOT NULL DEFAULT 'pending',
  "started_at" timestamptz NULL,
  "completed_at" timestamptz NULL,
  "duration_ms" bigint NULL,
  "error_message" character varying NULL,
  "iteration_strategy" character varying NOT NULL,
  "session_id" character varying NOT NULL,
  "stage_id" character varying NOT NULL,
  PRIMARY KEY ("execution_id"),
  CONSTRAINT "agent_executions_alert_sessions_agent_executions" FOREIGN KEY ("session_id") REFERENCES "public"."alert_sessions" ("session_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "agent_executions_stages_agent_executions" FOREIGN KEY ("stage_id") REFERENCES "public"."stages" ("stage_id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create index "agentexecution_session_id" to table: "agent_executions"
CREATE INDEX "agentexecution_session_id" ON "public"."agent_executions" ("session_id");
-- Create index "agentexecution_stage_id_agent_index" to table: "agent_executions"
CREATE UNIQUE INDEX "agentexecution_stage_id_agent_index" ON "public"."agent_executions" ("stage_id", "agent_index");
-- Create "events" table
CREATE TABLE "public"."events" (
  "id" bigint NOT NULL GENERATED BY DEFAULT AS IDENTITY,
  "channel" character varying NOT NULL,
  "payload" jsonb NOT NULL,
  "created_at" timestamptz NOT NULL,
  "session_id" character varying NOT NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "events_alert_sessions_events" FOREIGN KEY ("session_id") REFERENCES "public"."alert_sessions" ("session_id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create index "event_channel" to table: "events"
CREATE INDEX "event_channel" ON "public"."events" ("channel");
-- Create index "event_channel_id" to table: "events"
CREATE INDEX "event_channel_id" ON "public"."events" ("channel", "id");
-- Create index "event_created_at" to table: "events"
CREATE INDEX "event_created_at" ON "public"."events" ("created_at");
-- Create index "event_session_id" to table: "events"
CREATE INDEX "event_session_id" ON "public"."events" ("session_id");
-- Create "messages" table
CREATE TABLE "public"."messages" (
  "message_id" character varying NOT NULL,
  "sequence_number" bigint NOT NULL,
  "role" character varying NOT NULL,
  "content" text NOT NULL,
  "tool_calls" jsonb NULL,
  "tool_call_id" character varying NULL,
  "tool_name" character varying NULL,
  "created_at" timestamptz NOT NULL,
  "execution_id" character varying NOT NULL,
  "session_id" character varying NOT NULL,
  "stage_id" character varying NOT NULL,
  PRIMARY KEY ("message_id"),
  CONSTRAINT "messages_agent_executions_messages" FOREIGN KEY ("execution_id") REFERENCES "public"."agent_executions" ("execution_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "messages_alert_sessions_messages" FOREIGN KEY ("session_id") REFERENCES "public"."alert_sessions" ("session_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "messages_stages_messages" FOREIGN KEY ("stage_id") REFERENCES "public"."stages" ("stage_id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create index "message_execution_id_sequence_number" to table: "messages"
CREATE INDEX "message_execution_id_sequence_number" ON "public"."messages" ("execution_id", "sequence_number");
-- Create index "message_stage_id_execution_id" to table: "messages"
CREATE INDEX "message_stage_id_execution_id" ON "public"."messages" ("stage_id", "execution_id");
-- Create "llm_interactions" table
CREATE TABLE "public"."llm_interactions" (
  "interaction_id" character varying NOT NULL,
  "created_at" timestamptz NOT NULL,
  "interaction_type" character varying NOT NULL,
  "model_name" character varying NOT NULL,
  "llm_request" jsonb NOT NULL,
  "llm_response" jsonb NOT NULL,
  "thinking_content" text NULL,
  "response_metadata" jsonb NULL,
  "input_tokens" bigint NULL,
  "output_tokens" bigint NULL,
  "total_tokens" bigint NULL,
  "duration_ms" bigint NULL,
  "error_message" character varying NULL,
  "execution_id" character varying NOT NULL,
  "session_id" character varying NOT NULL,
  "last_message_id" character varying NULL,
  "stage_id" character varying NOT NULL,
  PRIMARY KEY ("interaction_id"),
  CONSTRAINT "llm_interactions_agent_executions_llm_interactions" FOREIGN KEY ("execution_id") REFERENCES "public"."agent_executions" ("execution_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "llm_interactions_alert_sessions_llm_interactions" FOREIGN KEY ("session_id") REFERENCES "public"."alert_sessions" ("session_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "llm_interactions_messages_llm_interactions" FOREIGN KEY ("last_message_id") REFERENCES "public"."messages" ("message_id") ON UPDATE NO ACTION ON DELETE SET NULL,
  CONSTRAINT "llm_interactions_stages_llm_interactions" FOREIGN KEY ("stage_id") REFERENCES "public"."stages" ("stage_id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create index "llminteraction_execution_id_created_at" to table: "llm_interactions"
CREATE INDEX "llminteraction_execution_id_created_at" ON "public"."llm_interactions" ("execution_id", "created_at");
-- Create index "llminteraction_stage_id_created_at" to table: "llm_interactions"
CREATE INDEX "llminteraction_stage_id_created_at" ON "public"."llm_interactions" ("stage_id", "created_at");
-- Create "mcp_interactions" table
CREATE TABLE "public"."mcp_interactions" (
  "interaction_id" character varying NOT NULL,
  "created_at" timestamptz NOT NULL,
  "interaction_type" character varying NOT NULL,
  "server_name" character varying NOT NULL,
  "tool_name" character varying NULL,
  "tool_arguments" jsonb NULL,
  "tool_result" jsonb NULL,
  "available_tools" jsonb NULL,
  "duration_ms" bigint NULL,
  "error_message" character varying NULL,
  "execution_id" character varying NOT NULL,
  "session_id" character varying NOT NULL,
  "stage_id" character varying NOT NULL,
  PRIMARY KEY ("interaction_id"),
  CONSTRAINT "mcp_interactions_agent_executions_mcp_interactions" FOREIGN KEY ("execution_id") REFERENCES "public"."agent_executions" ("execution_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "mcp_interactions_alert_sessions_mcp_interactions" FOREIGN KEY ("session_id") REFERENCES "public"."alert_sessions" ("session_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "mcp_interactions_stages_mcp_interactions" FOREIGN KEY ("stage_id") REFERENCES "public"."stages" ("stage_id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create index "mcpinteraction_execution_id_created_at" to table: "mcp_interactions"
CREATE INDEX "mcpinteraction_execution_id_created_at" ON "public"."mcp_interactions" ("execution_id", "created_at");
-- Create index "mcpinteraction_stage_id_created_at" to table: "mcp_interactions"
CREATE INDEX "mcpinteraction_stage_id_created_at" ON "public"."mcp_interactions" ("stage_id", "created_at");
-- Create "timeline_events" table
CREATE TABLE "public"."timeline_events" (
  "event_id" character varying NOT NULL,
  "sequence_number" bigint NOT NULL,
  "created_at" timestamptz NOT NULL,
  "updated_at" timestamptz NOT NULL,
  "event_type" character varying NOT NULL,
  "status" character varying NOT NULL DEFAULT 'streaming',
  "content" text NOT NULL,
  "metadata" jsonb NULL,
  "execution_id" character varying NOT NULL,
  "session_id" character varying NOT NULL,
  "llm_interaction_id" character varying NULL,
  "mcp_interaction_id" character varying NULL,
  "stage_id" character varying NOT NULL,
  PRIMARY KEY ("event_id"),
  CONSTRAINT "timeline_events_agent_executions_timeline_events" FOREIGN KEY ("execution_id") REFERENCES "public"."agent_executions" ("execution_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "timeline_events_alert_sessions_timeline_events" FOREIGN KEY ("session_id") REFERENCES "public"."alert_sessions" ("session_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "timeline_events_llm_interactions_timeline_events" FOREIGN KEY ("llm_interaction_id") REFERENCES "public"."llm_interactions" ("interaction_id") ON UPDATE NO ACTION ON DELETE SET NULL,
  CONSTRAINT "timeline_events_mcp_interactions_timeline_events" FOREIGN KEY ("mcp_interaction_id") REFERENCES "public"."mcp_interactions" ("interaction_id") ON UPDATE NO ACTION ON DELETE SET NULL,
  CONSTRAINT "timeline_events_stages_timeline_events" FOREIGN KEY ("stage_id") REFERENCES "public"."stages" ("stage_id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create index "timelineevent_created_at" to table: "timeline_events"
CREATE INDEX "timelineevent_created_at" ON "public"."timeline_events" ("created_at");
-- Create index "timelineevent_execution_id_sequence_number" to table: "timeline_events"
CREATE INDEX "timelineevent_execution_id_sequence_number" ON "public"."timeline_events" ("execution_id", "sequence_number");
-- Create index "timelineevent_session_id_sequence_number" to table: "timeline_events"
CREATE INDEX "timelineevent_session_id_sequence_number" ON "public"."timeline_events" ("session_id", "sequence_number");
-- Create index "timelineevent_stage_id_sequence_number" to table: "timeline_events"
CREATE INDEX "timelineevent_stage_id_sequence_number" ON "public"."timeline_events" ("stage_id", "sequence_number");
