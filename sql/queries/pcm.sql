-- name: CreateCanonicalVendor :one
INSERT INTO fignode.canonical_vendors (
    entity_id, realm_id, display_name, ice_number, default_account
) VALUES (
    $1, $2, $3, $4, $5
) RETURNING *;

-- name: CreateVendorAlias :one
INSERT INTO fignode.vendor_aliases (
    entity_id, realm_id, raw_variant, canonical_vendor_id, source, confidence_score
) VALUES (
    $1, $2, $3, $4, $5, $6
) RETURNING *;

-- name: GetVendorAlias :one
SELECT * FROM fignode.vendor_aliases
WHERE entity_id = $1 AND raw_variant = $2 LIMIT 1;

-- name: InsertOrderReconciliation :one
INSERT INTO fignode.order_reconciliations (
    entity_id, realm_id, bank_transaction_id, facture_id, bl_id, bc_id, status
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
) RETURNING *;

-- name: UpdateOrderReconciliationStatus :one
UPDATE fignode.order_reconciliations
SET status = $2, updated_at = NOW()
WHERE id = $1 RETURNING *;
