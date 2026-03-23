CREATE TABLE IF NOT EXISTS admin_setup_state (
  id SMALLINT PRIMARY KEY CHECK (id = 1),
  initialized BOOLEAN NOT NULL DEFAULT FALSE,
  initialized_admin_username VARCHAR(64) NOT NULL DEFAULT '',
  default_merchant_no VARCHAR(16) NOT NULL DEFAULT '',
  initialized_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO admin_setup_state (
  id,
  initialized,
  initialized_admin_username,
  default_merchant_no
) VALUES (
  1,
  FALSE,
  '',
  ''
)
ON CONFLICT (id) DO NOTHING;
