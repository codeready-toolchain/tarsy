BEGIN;
ALTER TABLE "public"."alert_sessions" ADD COLUMN "labels" jsonb NULL;
COMMIT;
