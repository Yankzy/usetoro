-- name: CreateRuleGroup :one
INSERT INTO shadow_erp.rule_groups (
    realm_id, name, logic, priority, active,
    target_entity_id, requires_review, allocations, parent_id, direction
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10
) RETURNING *;

-- name: GetActiveRuleGroupsByRealm :many
SELECT * FROM shadow_erp.rule_groups 
WHERE realm_id = $1 AND active = true
ORDER BY priority ASC, id ASC;

-- name: GetRuleGroupByRealmAndName :one
SELECT * FROM shadow_erp.rule_groups
WHERE realm_id = $1 AND name = $2;

-- name: UpdateRuleGroupKeywords :exec
UPDATE shadow_erp.rule_groups
SET keywords = $2, updated_at = timezone('utc', now())
WHERE id = $1;

-- name: CreateRuleCondition :one
INSERT INTO shadow_erp.rule_conditions (
    rule_group_id, field, operator, value
) VALUES (
    $1, $2, $3, $4
) RETURNING *;

-- name: GetConditionsByRuleGroups :many
SELECT * FROM shadow_erp.rule_conditions
WHERE rule_group_id = ANY(@rule_group_ids::int[])
ORDER BY rule_group_id ASC, id ASC;

-- name: GetRuleConditionByExample :one
SELECT * FROM shadow_erp.rule_conditions
WHERE rule_group_id = $1 AND field = $2 AND operator = $3 AND value = $4;

-- name: CreateRuleAuditLog :one
INSERT INTO shadow_erp.rule_audit_logs (
    realm_id, transaction_id, rule_group_id, matched, match_info, human_readable_reason
) VALUES (
    $1, $2, $3, $4, $5, $6
) RETURNING *;

-- name: GetRuleAuditLogsByTransaction :many
SELECT * FROM shadow_erp.rule_audit_logs
WHERE transaction_id = $1
ORDER BY created_at DESC;

-- name: GetAccountByID :one
SELECT * FROM shadow_erp.accounts WHERE id = $1;

-- name: GetVendorByID :one
SELECT * FROM shadow_erp.vendors WHERE id = $1;
