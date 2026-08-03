-- +goose Up
-- Fix the seeded data to use the exact realm_id that the code extracts (which includes the 'rap_' prefix from the email alias)

UPDATE shadow_erp.sage_import_templates
SET realm_id = 'rap_test100'
WHERE realm_id = 'test-realm-1';

UPDATE shadow_erp.client_dossiers
SET realm_id = 'rap_test100'
WHERE realm_id = 'test-realm-1';

UPDATE fignode.canonical_vendors
SET realm_id = 'rap_test100'
WHERE realm_id = 'test-realm-1';

-- +goose Down
-- Revert
UPDATE fignode.canonical_vendors SET realm_id = 'test-realm-1' WHERE realm_id = 'rap_test100';
UPDATE shadow_erp.client_dossiers SET realm_id = 'test-realm-1' WHERE realm_id = 'rap_test100';
UPDATE shadow_erp.sage_import_templates SET realm_id = 'test-realm-1' WHERE realm_id = 'rap_test100';
