BEGIN;
ALTER TABLE "public"."alert_sessions" ADD COLUMN "post_investigation_skips" jsonb NULL;
COMMIT;
