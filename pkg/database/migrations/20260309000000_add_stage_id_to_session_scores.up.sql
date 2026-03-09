-- modify "session_scores" table: add stage_id FK
ALTER TABLE "public"."session_scores" ADD COLUMN "stage_id" character varying NULL, ADD CONSTRAINT "session_scores_stages_session_scores" FOREIGN KEY ("stage_id") REFERENCES "public"."stages" ("stage_id") ON UPDATE NO ACTION ON DELETE SET NULL;
