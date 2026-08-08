-- name: CreateFact :one
INSERT INTO toro_core.enterprise_facts (
    realm_id, namespace, entity_type, uri, payload
) VALUES (
    $1, $2, $3, $4, $5
) RETURNING fact_id, realm_id, namespace, entity_type, uri, payload, created_at;

-- name: GetFactByURI :one
SELECT fact_id, realm_id, namespace, entity_type, uri, payload, created_at
FROM toro_core.enterprise_facts
WHERE realm_id = $1 AND uri = $2;

-- name: GetFactByID :one
SELECT fact_id, realm_id, namespace, entity_type, uri, payload, created_at
FROM toro_core.enterprise_facts
WHERE fact_id = $1;

-- name: ListFactsByRealmAndNamespace :many
SELECT fact_id, realm_id, namespace, entity_type, uri, payload, created_at
FROM toro_core.enterprise_facts
WHERE realm_id = $1 
  AND (namespace = $2 OR $2 = '' OR $2 = '*')
  AND (entity_type = $3 OR $3 = '')
ORDER BY created_at DESC;

-- name: CreateRelationship :one
INSERT INTO toro_core.enterprise_relationships (
    realm_id, namespace, from_fact_id, to_fact_id, relation_type, weight
) VALUES (
    $1, $2, $3, $4, $5, $6
) ON CONFLICT ON CONSTRAINT unique_relation DO UPDATE SET
    weight = EXCLUDED.weight
RETURNING relationship_id, realm_id, namespace, from_fact_id, to_fact_id, relation_type, weight, created_at;

-- name: ListRelationshipsFromFact :many
SELECT r.relationship_id, r.realm_id, r.namespace, r.from_fact_id, r.to_fact_id, r.relation_type, r.weight, r.created_at,
       f.entity_type AS to_entity_type, f.uri AS to_uri, f.payload AS to_payload
FROM toro_core.enterprise_relationships r
JOIN toro_core.enterprise_facts f ON r.to_fact_id = f.fact_id
WHERE r.from_fact_id = $1;

-- name: ListRelationshipsToFact :many
SELECT r.relationship_id, r.realm_id, r.namespace, r.from_fact_id, r.to_fact_id, r.relation_type, r.weight, r.created_at,
       f.entity_type AS from_entity_type, f.uri AS from_uri, f.payload AS from_payload
FROM toro_core.enterprise_relationships r
JOIN toro_core.enterprise_facts f ON r.from_fact_id = f.fact_id
WHERE r.to_fact_id = $1;

-- name: CreateDocument :one
INSERT INTO toro_core.documents (
    realm_id, document_type, file_name, mime_type, s3_url, ocr_status, raw_ocr_json, extracted_text, sender_email, source_channel
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10
) RETURNING id, realm_id, document_type, file_name, mime_type, s3_url, ocr_status, raw_ocr_json, extracted_text, sender_email, source_channel, processed_at, created_at, updated_at;

-- name: UpdateDocumentOCRStatus :one
UPDATE toro_core.documents
SET ocr_status = $2,
    raw_ocr_json = $3,
    extracted_text = $4,
    processed_at = NOW(),
    updated_at = NOW()
WHERE id = $1
RETURNING id, realm_id, document_type, file_name, mime_type, s3_url, ocr_status, raw_ocr_json, extracted_text, sender_email, source_channel, processed_at, created_at, updated_at;

-- name: GetDocumentByID :one
SELECT id, realm_id, document_type, file_name, mime_type, s3_url, ocr_status, raw_ocr_json, extracted_text, sender_email, source_channel, processed_at, created_at, updated_at
FROM toro_core.documents
WHERE id = $1;

-- name: ListPendingDocuments :many
SELECT id, realm_id, document_type, file_name, mime_type, s3_url, ocr_status, raw_ocr_json, extracted_text, sender_email, source_channel, processed_at, created_at, updated_at
FROM toro_core.documents
WHERE ocr_status = 'PENDING'
ORDER BY created_at ASC
LIMIT $1;
