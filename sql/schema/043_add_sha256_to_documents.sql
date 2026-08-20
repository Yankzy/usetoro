-- +goose Up
ALTER TABLE toro_core.documents ADD COLUMN IF NOT EXISTS sha256 TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS idx_documents_session_id_sha256
    ON toro_core.documents (session_id, sha256)
    WHERE sha256 IS NOT NULL AND sha256 != '';

-- +goose Down
DROP INDEX IF EXISTS idx_documents_session_id_sha256;
ALTER TABLE toro_core.documents DROP COLUMN IF EXISTS sha256;
