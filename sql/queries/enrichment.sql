-- name: GetMasterMerchantByICE :one
SELECT id, normalized_name, legal_name, country_code, merchant_category, ice, identifiant_fiscal, registre_commerce, cnss_number, primary_domain, logo_url, default_pcgm_account, default_tva_rate, default_tva_account, is_tva_deductible, is_foreign_service, ras_applicable, ras_rate, confidence_weight, created_at, updated_at
FROM toro_core.master_merchants
WHERE ice = $1
LIMIT 1;

-- name: GetMasterMerchantByID :one
SELECT id, normalized_name, legal_name, country_code, merchant_category, ice, identifiant_fiscal, registre_commerce, cnss_number, primary_domain, logo_url, default_pcgm_account, default_tva_rate, default_tva_account, is_tva_deductible, is_foreign_service, ras_applicable, ras_rate, confidence_weight, created_at, updated_at
FROM toro_core.master_merchants
WHERE id = $1;

-- name: FindMerchantByExactMultilingualAlias :one
SELECT m.id, m.normalized_name, m.legal_name, m.country_code, m.merchant_category, m.ice, m.identifiant_fiscal, m.registre_commerce, m.cnss_number, m.primary_domain, m.logo_url, m.default_pcgm_account, m.default_tva_rate, m.default_tva_account, m.is_tva_deductible, m.is_foreign_service, m.ras_applicable, m.ras_rate, m.confidence_weight,
       a.alias_variant, a.script_type, a.language_code, a.confidence_score
FROM toro_core.moroccan_merchant_multilingual_aliases a
JOIN toro_core.master_merchants m ON a.master_merchant_id = m.id
WHERE LOWER(a.alias_variant) = LOWER($1)
ORDER BY a.is_primary DESC, a.confidence_score DESC
LIMIT 1;

-- name: FindMerchantByFuzzyMultilingualAlias :many
SELECT m.id, m.normalized_name, m.legal_name, m.country_code, m.merchant_category, m.ice, m.identifiant_fiscal, m.registre_commerce, m.cnss_number, m.primary_domain, m.logo_url, m.default_pcgm_account, m.default_tva_rate, m.default_tva_account, m.is_tva_deductible, m.is_foreign_service, m.ras_applicable, m.ras_rate, m.confidence_weight,
       a.alias_variant, a.script_type, a.language_code, a.confidence_score,
       similarity(a.alias_variant, $1) AS match_similarity
FROM toro_core.moroccan_merchant_multilingual_aliases a
JOIN toro_core.master_merchants m ON a.master_merchant_id = m.id
WHERE a.alias_variant % $1
ORDER BY match_similarity DESC, a.is_primary DESC
LIMIT 5;

-- name: ListAllMultilingualAliases :many
SELECT a.id, a.master_merchant_id, a.alias_variant, a.script_type, a.language_code, a.is_primary, a.confidence_score,
       m.normalized_name, m.merchant_category, m.ice, m.default_pcgm_account, m.default_tva_rate, m.default_tva_account, m.is_foreign_service, m.ras_applicable, m.ras_rate
FROM toro_core.moroccan_merchant_multilingual_aliases a
JOIN toro_core.master_merchants m ON a.master_merchant_id = m.id;

-- name: GetNonResidentForeignProviderByMerchantID :one
SELECT id, master_merchant_id, provider_name, headquarters_country, tax_residency_status, vat_withholding_rate, service_type, pcgm_expense_account, pcgm_withholding_account, is_active, statutory_legal_basis, created_at, updated_at
FROM toro_core.non_resident_foreign_providers
WHERE master_merchant_id = $1 AND is_active = TRUE
LIMIT 1;

-- name: ListActiveNonResidentForeignProviders :many
SELECT p.id, p.master_merchant_id, p.provider_name, p.headquarters_country, p.tax_residency_status, p.vat_withholding_rate, p.service_type, p.pcgm_expense_account, p.pcgm_withholding_account, p.is_active,
       m.normalized_name, m.default_pcgm_account, m.default_tva_rate
FROM toro_core.non_resident_foreign_providers p
JOIN toro_core.master_merchants m ON p.master_merchant_id = m.id
WHERE p.is_active = TRUE;

-- name: UpsertForeignServiceRunningTotal :one
INSERT INTO toro_core.foreign_service_running_totals (
    realm_id, provider_id, fiscal_year, fiscal_month,
    cumulative_gross_invoiced_mad, cumulative_ras_withheld_mad, cumulative_net_paid_mad,
    transaction_count, last_transaction_at
) VALUES (
    $1, $2, $3, $4,
    $5, $6, $7,
    1, NOW()
) ON CONFLICT (realm_id, provider_id, fiscal_year, fiscal_month) DO UPDATE SET
    cumulative_gross_invoiced_mad = toro_core.foreign_service_running_totals.cumulative_gross_invoiced_mad + EXCLUDED.cumulative_gross_invoiced_mad,
    cumulative_ras_withheld_mad   = toro_core.foreign_service_running_totals.cumulative_ras_withheld_mad + EXCLUDED.cumulative_ras_withheld_mad,
    cumulative_net_paid_mad       = toro_core.foreign_service_running_totals.cumulative_net_paid_mad + EXCLUDED.cumulative_net_paid_mad,
    transaction_count             = toro_core.foreign_service_running_totals.transaction_count + 1,
    last_transaction_at           = NOW(),
    updated_at                    = NOW()
RETURNING id, realm_id, provider_id, fiscal_year, fiscal_month, cumulative_gross_invoiced_mad, cumulative_ras_withheld_mad, cumulative_net_paid_mad, transaction_count, dgi_declaration_status, last_transaction_at;

-- name: GetForeignServiceRunningTotalForPeriod :one
SELECT id, realm_id, provider_id, fiscal_year, fiscal_month, cumulative_gross_invoiced_mad, cumulative_ras_withheld_mad, cumulative_net_paid_mad, transaction_count, dgi_declaration_status, last_transaction_at
FROM toro_core.foreign_service_running_totals
WHERE realm_id = $1 AND provider_id = $2 AND fiscal_year = $3 AND fiscal_month = $4;

-- name: GetAnnualForeignServiceRunningTotal :one
SELECT COALESCE(SUM(cumulative_gross_invoiced_mad), 0)::NUMERIC(15,2) AS annual_gross_mad,
       COALESCE(SUM(cumulative_ras_withheld_mad), 0)::NUMERIC(15,2)   AS annual_ras_mad,
       COALESCE(SUM(cumulative_net_paid_mad), 0)::NUMERIC(15,2)       AS annual_net_mad,
       COALESCE(SUM(transaction_count), 0)::INT                       AS annual_transaction_count
FROM toro_core.foreign_service_running_totals
WHERE realm_id = $1 AND provider_id = $2 AND fiscal_year = $3;

-- name: ListForeignServiceRunningTotalsForTenantPeriod :many
SELECT t.id, t.realm_id, t.provider_id, t.fiscal_year, t.fiscal_month, t.cumulative_gross_invoiced_mad, t.cumulative_ras_withheld_mad, t.cumulative_net_paid_mad, t.transaction_count, t.dgi_declaration_status, t.last_transaction_at,
       p.provider_name, p.headquarters_country, p.vat_withholding_rate, p.service_type
FROM toro_core.foreign_service_running_totals t
JOIN toro_core.non_resident_foreign_providers p ON t.provider_id = p.id
WHERE t.realm_id = $1 AND t.fiscal_year = $2 AND t.fiscal_month = $3
ORDER BY t.cumulative_gross_invoiced_mad DESC;

-- =========================================================================
-- Dynamic Seeding & Continuous Learning Queries
-- =========================================================================

-- name: UpsertMasterMerchant :one
INSERT INTO toro_core.master_merchants (
    id, normalized_name, legal_name, country_code, merchant_category,
    ice, identifiant_fiscal, registre_commerce, cnss_number, primary_domain, logo_url,
    default_pcgm_account, default_tva_rate, default_tva_account, is_tva_deductible,
    is_foreign_service, ras_applicable, ras_rate, confidence_weight
) VALUES (
    COALESCE(sqlc.narg('id')::UUID, gen_random_uuid()),
    $1, $2, $3, $4,
    $5, $6, $7, $8, $9, $10,
    $11, $12, $13, $14,
    $15, $16, $17, $18
) ON CONFLICT (id) DO UPDATE SET
    normalized_name = EXCLUDED.normalized_name,
    legal_name = EXCLUDED.legal_name,
    merchant_category = EXCLUDED.merchant_category,
    ice = COALESCE(EXCLUDED.ice, toro_core.master_merchants.ice),
    identifiant_fiscal = COALESCE(EXCLUDED.identifiant_fiscal, toro_core.master_merchants.identifiant_fiscal),
    default_pcgm_account = EXCLUDED.default_pcgm_account,
    default_tva_rate = EXCLUDED.default_tva_rate,
    is_foreign_service = EXCLUDED.is_foreign_service,
    ras_applicable = EXCLUDED.ras_applicable,
    ras_rate = EXCLUDED.ras_rate,
    updated_at = NOW()
RETURNING id, normalized_name, legal_name, country_code, merchant_category, ice, default_pcgm_account, default_tva_rate;

-- name: CreateMerchantMultilingualAlias :one
INSERT INTO toro_core.moroccan_merchant_multilingual_aliases (
    master_merchant_id, alias_variant, script_type, language_code, is_primary, confidence_score
) VALUES (
    $1, $2, $3, $4, $5, $6
) RETURNING id, master_merchant_id, alias_variant, script_type, language_code, is_primary, confidence_score;

-- name: CreateCoreMasterPattern :one
INSERT INTO toro_core.master_patterns (
    master_merchant_id, cleaned_stem, pattern_type, regex_pattern, is_intermediary
) VALUES (
    $1, $2, $3, $4, $5
) RETURNING id, master_merchant_id, cleaned_stem, pattern_type, regex_pattern, is_intermediary;

