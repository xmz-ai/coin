CREATE TABLE IF NOT EXISTS applied_change_counter (
  id SMALLINT PRIMARY KEY CHECK (id = 1),
  value BIGINT NOT NULL DEFAULT 0
);

INSERT INTO applied_change_counter(id, value)
VALUES (1, 0)
ON CONFLICT (id) DO NOTHING;
