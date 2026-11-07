-- 0015: persistent PII key-rotation marker. cmd/piirotate writes this row in
-- the same transaction as the re-encryption, so autodeploy can refuse to roll
-- back a pre-split binary after the database has been rotated.
CREATE TABLE IF NOT EXISTS pii_rotation (
    id          boolean PRIMARY KEY DEFAULT true CHECK (id),
    rotated_at  timestamptz NOT NULL DEFAULT now(),
    key_version text NOT NULL DEFAULT 'v2'
);
