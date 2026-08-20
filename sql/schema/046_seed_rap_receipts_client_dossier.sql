-- +goose Up
-- +goose StatementBegin
INSERT INTO shadow_erp.client_dossiers (id, realm_id, fiduciaire_id, dossier_code, company_name, ice_number)
VALUES (
    '88888888-9999-4444-aaaa-bbbbbbbbbbbb',
    'rap_receipts',
    '11111111-2222-3333-4444-555555555555',
    'rap_receipts',
    'RAP Receipts Client Dossier',
    '009999999000089'
) ON CONFLICT (realm_id) DO NOTHING;

INSERT INTO shadow_erp.client_dossiers (id, realm_id, fiduciaire_id, dossier_code, company_name, ice_number)
VALUES (
    '77777777-9999-4444-aaaa-bbbbbbbbbbbb',
    'receipts',
    '11111111-2222-3333-4444-555555555555',
    'receipts',
    'Receipts Alias Client Dossier',
    '009999999000090'
) ON CONFLICT (realm_id) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM shadow_erp.client_dossiers WHERE realm_id IN ('rap_receipts', 'receipts');
-- +goose StatementEnd
