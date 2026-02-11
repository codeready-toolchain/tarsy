-- Create index "chats_session_id_key" to table: "chats"
CREATE UNIQUE INDEX "chats_session_id_key" ON "public"."chats" ("session_id");
-- Modify "timeline_events" table
ALTER TABLE "public"."timeline_events" ALTER COLUMN "execution_id" DROP NOT NULL, ALTER COLUMN "stage_id" DROP NOT NULL;
