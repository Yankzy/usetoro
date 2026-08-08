-- +goose Up

-- =========================================================================
-- Layer 1: Authoritative Fact Nodes (toro_core.enterprise_facts)
-- =========================================================================
CREATE TABLE IF NOT EXISTS toro_core.enterprise_facts (
    fact_id    UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    realm_id   TEXT        NOT NULL,
    namespace  TEXT        NOT NULL DEFAULT 'general',
    entity_type TEXT       NOT NULL, -- e.g. 'invoice', 'supplier', 'bank_account'
    uri        TEXT        UNIQUE NOT NULL, -- e.g. 'fact:accounting:invoice:284'
    payload    JSONB       NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_facts_realm_ns_type 
    ON toro_core.enterprise_facts (realm_id, namespace, entity_type);

-- =========================================================================
-- Layer 2: Directional Relationships (toro_core.enterprise_relationships)
-- =========================================================================
CREATE TABLE IF NOT EXISTS toro_core.enterprise_relationships (
    relationship_id UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    realm_id        TEXT        NOT NULL,
    namespace       TEXT        NOT NULL DEFAULT 'general',
    from_fact_id    UUID        NOT NULL REFERENCES toro_core.enterprise_facts(fact_id) ON DELETE CASCADE,
    to_fact_id      UUID        NOT NULL REFERENCES toro_core.enterprise_facts(fact_id) ON DELETE CASCADE,
    relation_type   TEXT        NOT NULL, -- e.g. 'ISSUED_BY', 'PAID_BY', 'SETTLES'
    weight          FLOAT8      NOT NULL DEFAULT 1.0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT unique_relation UNIQUE (realm_id, namespace, from_fact_id, to_fact_id, relation_type)
);

CREATE INDEX IF NOT EXISTS idx_rel_from_to 
    ON toro_core.enterprise_relationships (from_fact_id, to_fact_id);

-- =========================================================================
-- Master Documents Table (toro_core.documents)
-- =========================================================================
CREATE TABLE IF NOT EXISTS toro_core.documents (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    realm_id       TEXT        NOT NULL,
    document_type  TEXT        NOT NULL CHECK (document_type IN ('INVOICE', 'RECEIPT', 'BANK_STATEMENT', 'BILL', 'TAX_FORM', 'OTHER')),
    file_name      TEXT        NOT NULL,
    mime_type      TEXT        NOT NULL,
    s3_url         TEXT        NOT NULL,
    ocr_status     TEXT        NOT NULL DEFAULT 'PENDING' CHECK (ocr_status IN ('PENDING', 'PROCESSING', 'PROCESSED', 'FAILED')),
    raw_ocr_json   JSONB       NOT NULL DEFAULT '{}',
    extracted_text TEXT,
    sender_email   TEXT,
    source_channel TEXT        NOT NULL DEFAULT 'EMAIL' CHECK (source_channel IN ('EMAIL', 'WEB_UPLOAD', 'MOBILE_SCAN', 'API_SYNC')),
    processed_at   TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_documents_realm_status 
    ON toro_core.documents (realm_id, ocr_status);

CREATE INDEX IF NOT EXISTS idx_documents_type 
    ON toro_core.documents (realm_id, document_type);

CREATE TRIGGER update_toro_core_documents_updated_at
    BEFORE UPDATE ON toro_core.documents
    FOR EACH ROW EXECUTE FUNCTION toro_core.update_updated_at_column();

-- +goose Down
DROP TRIGGER IF EXISTS update_toro_core_documents_updated_at ON toro_core.documents;
DROP INDEX IF EXISTS idx_documents_type;
DROP INDEX IF EXISTS idx_documents_realm_status;
DROP TABLE IF EXISTS toro_core.documents;

DROP INDEX IF EXISTS idx_rel_from_to;
DROP TABLE IF EXISTS toro_core.enterprise_relationships;

DROP INDEX IF EXISTS idx_facts_realm_ns_type;
DROP TABLE IF EXISTS toro_core.enterprise_facts;
