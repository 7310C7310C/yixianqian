-- 0014: explicit sensitive-PII consent at registration (PIPL 14/17).
-- Additive only. Existing rows keep NULL/empty and are not treated as consent.
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS privacy_consent_at timestamptz,
    ADD COLUMN IF NOT EXISTS privacy_consent_version text NOT NULL DEFAULT '';
