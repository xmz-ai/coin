ALTER TABLE outbox_event
  DROP CONSTRAINT IF EXISTS outbox_event_pkey;

ALTER TABLE outbox_event
  ADD CONSTRAINT outbox_event_pkey PRIMARY KEY (event_id);

ALTER TABLE outbox_event
  DROP CONSTRAINT IF EXISTS outbox_event_event_id_key;

ALTER TABLE outbox_event
  ALTER COLUMN id DROP DEFAULT;

DROP SEQUENCE IF EXISTS outbox_event_id_seq;

ALTER TABLE outbox_event
  DROP COLUMN IF EXISTS id;
