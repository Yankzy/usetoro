-- +goose Up
-- AI Vector Metadata and Learning Tables

-- Track Pinecone sync state per realm
CREATE TABLE IF NOT EXISTS qbo.vector_sync_state (
    realm_id TEXT PRIMARY KEY,
    last_coa_sync TIMESTAMPTZ,
    last_vendor_sync TIMESTAMPTZ,
    last_customer_sync TIMESTAMPTZ,
    coa_vector_count INT DEFAULT 0,
    vendor_vector_count INT DEFAULT 0,
    customer_vector_count INT DEFAULT 0,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

COMMENT ON TABLE qbo.vector_sync_state IS 'Tracks Pinecone vector database sync state per QBO realm';
COMMENT ON COLUMN qbo.vector_sync_state.last_coa_sync IS 'Last Chart of Accounts sync to Pinecone';
COMMENT ON COLUMN qbo.vector_sync_state.coa_vector_count IS 'Number of account vectors in Pinecone';

-- Track AI learning from user corrections
CREATE TABLE IF NOT EXISTS qbo.ai_corrections (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    realm_id TEXT NOT NULL,
    user_id UUID REFERENCES users(id),     -- Who made the correction
    raw_input TEXT NOT NULL,              -- Original raw text (e.g., "STAPLS #452")
    ai_prediction TEXT,                   -- What the AI predicted (e.g., vendor_id "100")
    user_correction TEXT NOT NULL,        -- What the user corrected to (e.g., vendor_id "101")
    correction_type TEXT NOT NULL,        -- 'vendor', 'customer', 'account'
    confidence_score DECIMAL(3,2),        -- AI's original confidence (0.00-1.00)
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_ai_corrections_user ON qbo.ai_corrections(user_id);
CREATE INDEX IF NOT EXISTS idx_ai_corrections_realm ON qbo.ai_corrections(realm_id, correction_type);
CREATE INDEX IF NOT EXISTS idx_ai_corrections_created ON qbo.ai_corrections(created_at DESC);

COMMENT ON TABLE qbo.ai_corrections IS 'Records when users correct AI predictions for learning and synonym updates';
COMMENT ON COLUMN qbo.ai_corrections.raw_input IS 'The original text that AI tried to match';
COMMENT ON COLUMN qbo.ai_corrections.user_correction IS 'The correct entity ID provided by user';

-- +goose Down
DROP TABLE IF EXISTS qbo.ai_corrections;
DROP TABLE IF EXISTS qbo.vector_sync_state;
