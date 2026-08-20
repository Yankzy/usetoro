-- +goose Up

-- =========================================================================
-- Layer 1: Authoritative Fact Nodes (toro_core.enterprise_facts)
-- =========================================================================
CREATE TABLE IF NOT EXISTS toro_core.enterprise_facts (
    fact_id    UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id   TEXT        NOT NULL,
    namespace  TEXT        NOT NULL DEFAULT 'general',
    entity_type TEXT       NOT NULL, -- e.g. 'invoice', 'supplier', 'bank_account'
    uri        TEXT        NOT NULL, -- e.g. 'fact:accounting:invoice:284'
    payload    JSONB       NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT unique_session_uri UNIQUE (session_id, uri)
);

CREATE INDEX IF NOT EXISTS idx_facts_session_ns_type 
    ON toro_core.enterprise_facts (session_id, namespace, entity_type);

CREATE TRIGGER update_toro_core_enterprise_facts_updated_at
    BEFORE UPDATE ON toro_core.enterprise_facts
    FOR EACH ROW EXECUTE FUNCTION toro_core.update_updated_at_column();

-- =========================================================================
-- Layer 2: Directional Relationships (toro_core.enterprise_relationships)
-- =========================================================================
CREATE TABLE IF NOT EXISTS toro_core.enterprise_relationships (
    relationship_id UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id        TEXT        NOT NULL,
    namespace       TEXT        NOT NULL DEFAULT 'general',
    from_fact_id    UUID        NOT NULL REFERENCES toro_core.enterprise_facts(fact_id) ON DELETE CASCADE,
    to_fact_id      UUID        NOT NULL REFERENCES toro_core.enterprise_facts(fact_id) ON DELETE CASCADE,
    relation_type   TEXT        NOT NULL, -- e.g. 'ISSUED_BY', 'PAID_BY', 'SETTLES'
    weight          FLOAT8      NOT NULL DEFAULT 1.0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT unique_relation UNIQUE (session_id, namespace, from_fact_id, to_fact_id, relation_type)
);

CREATE INDEX IF NOT EXISTS idx_rel_from_to 
    ON toro_core.enterprise_relationships (from_fact_id, to_fact_id);

CREATE TRIGGER update_toro_core_enterprise_relationships_updated_at
    BEFORE UPDATE ON toro_core.enterprise_relationships
    FOR EACH ROW EXECUTE FUNCTION toro_core.update_updated_at_column();

-- =========================================================================
-- Master Documents Table (toro_core.documents)
-- =========================================================================
CREATE TABLE IF NOT EXISTS toro_core.documents (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id       TEXT        NOT NULL,
    document_type  TEXT        NOT NULL CHECK (document_type IN ('INVOICE', 'RECEIPT', 'BANK_STATEMENT', 'BILL', 'TAX_FORM', 'OTHER')),
    file_name      TEXT        NOT NULL,
    mime_type      TEXT        NOT NULL,
    s3_url         TEXT        NOT NULL,
    sha256         TEXT,
    ocr_status     TEXT        NOT NULL DEFAULT 'PENDING' CHECK (ocr_status IN ('PENDING', 'PROCESSING', 'OCR_SUCCESS', 'EMBEDDINGS_SUCCESS', 'FAILED')),
    raw_ocr_json   JSONB       NOT NULL DEFAULT '{}',
    extracted_text TEXT,
    sender_email   TEXT,
    source_channel TEXT        NOT NULL DEFAULT 'EMAIL' CHECK (source_channel IN ('EMAIL', 'WEB_UPLOAD', 'MOBILE_SCAN', 'API_SYNC')),
    metadata       JSONB       NOT NULL DEFAULT '{}',
    processed_at   TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_documents_session_status 
    ON toro_core.documents (session_id, ocr_status);

CREATE INDEX IF NOT EXISTS idx_documents_type 
    ON toro_core.documents (session_id, document_type);

CREATE UNIQUE INDEX IF NOT EXISTS idx_documents_session_sha256
    ON toro_core.documents (session_id, sha256)
    WHERE sha256 IS NOT NULL AND sha256 != '';

CREATE TRIGGER update_toro_core_documents_updated_at
    BEFORE UPDATE ON toro_core.documents
    FOR EACH ROW EXECUTE FUNCTION toro_core.update_updated_at_column();

-- +goose Down
DROP TRIGGER IF EXISTS update_toro_core_documents_updated_at ON toro_core.documents;
DROP INDEX IF EXISTS idx_documents_type;
DROP INDEX IF EXISTS idx_documents_session_status;
DROP TABLE IF EXISTS toro_core.documents;

DROP TRIGGER IF EXISTS update_toro_core_enterprise_relationships_updated_at ON toro_core.enterprise_relationships;
DROP INDEX IF EXISTS idx_rel_from_to;
DROP TABLE IF EXISTS toro_core.enterprise_relationships;

DROP TRIGGER IF EXISTS update_toro_core_enterprise_facts_updated_at ON toro_core.enterprise_facts;
DROP INDEX IF EXISTS idx_facts_session_ns_type;
DROP TABLE IF EXISTS toro_core.enterprise_facts;
