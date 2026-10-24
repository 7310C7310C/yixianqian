-- Deterministic HMAC of the identity number, used only to enforce
-- "one account per ID card". The ciphertext itself is non-deterministic
-- (AES-GCM random nonce) and cannot be compared for uniqueness.
--
-- Existing rows keep a NULL hash. They are not backfilled here because the
-- plaintext requires the deployment secret; a row with a valid ciphertext is
-- already gated as verified. New registrations and profile backfills fill it.
ALTER TABLE users ADD COLUMN IF NOT EXISTS id_card_hash text;

CREATE UNIQUE INDEX IF NOT EXISTS users_id_card_hash_uidx
    ON users (id_card_hash) WHERE id_card_hash IS NOT NULL;
