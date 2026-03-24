ALTER TABLE outbox_event
  ADD COLUMN IF NOT EXISTS id BIGINT;

CREATE SEQUENCE IF NOT EXISTS outbox_event_id_seq;
ALTER TABLE outbox_event
  ALTER COLUMN id SET DEFAULT nextval('outbox_event_id_seq');
ALTER SEQUENCE outbox_event_id_seq OWNED BY outbox_event.id;

UPDATE outbox_event
SET id = nextval('outbox_event_id_seq')
WHERE id IS NULL;

ALTER TABLE outbox_event
  ALTER COLUMN id SET NOT NULL;

ALTER TABLE outbox_event
  DROP CONSTRAINT IF EXISTS outbox_event_pkey;

ALTER TABLE outbox_event
  ADD CONSTRAINT outbox_event_pkey PRIMARY KEY (id);

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1
    FROM pg_constraint
    WHERE conname = 'outbox_event_event_id_key'
  ) THEN
    ALTER TABLE outbox_event
      ADD CONSTRAINT outbox_event_event_id_key UNIQUE (event_id);
  END IF;
END
$$;
