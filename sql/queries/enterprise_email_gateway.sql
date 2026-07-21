-- name: CreateEnterpriseDomain :one
INSERT INTO toro_core.enterprise_domains (
    entity_id,
    domain_name,
    dkim_private_key_encrypted,
    status
) VALUES (
    $1, $2, $3, $4
) RETURNING *;

-- name: GetEnterpriseDomainByName :one
SELECT * FROM toro_core.enterprise_domains
WHERE domain_name = $1;

-- name: GetEnterpriseDomainsByEntity :many
SELECT * FROM toro_core.enterprise_domains
WHERE entity_id = $1
ORDER BY created_at DESC;

-- name: UpdateEnterpriseDomainStatus :exec
UPDATE toro_core.enterprise_domains
SET status = $2
WHERE id = $1;

-- name: DeleteEnterpriseDomain :exec
DELETE FROM toro_core.enterprise_domains
WHERE id = $1;

-- name: CreateEnterpriseAgentAlias :one
INSERT INTO toro_core.enterprise_agent_aliases (
    domain_id,
    agent_alias,
    target_did
) VALUES (
    $1, $2, $3
) RETURNING *;

-- name: GetEnterpriseAgentAlias :one
SELECT * FROM toro_core.enterprise_agent_aliases
WHERE domain_id = $1 AND agent_alias = $2;

-- name: GetEnterpriseAgentAliasesByDomain :many
SELECT * FROM toro_core.enterprise_agent_aliases
WHERE domain_id = $1
ORDER BY agent_alias ASC;

-- name: DeleteEnterpriseAgentAlias :exec
DELETE FROM toro_core.enterprise_agent_aliases
WHERE id = $1;

-- name: ResolveAgentByAliasAndDomain :one
SELECT a.target_did, d.entity_id
FROM toro_core.enterprise_agent_aliases a
JOIN toro_core.enterprise_domains d ON a.domain_id = d.id
WHERE d.domain_name = $1 AND a.agent_alias = $2 AND d.status = 'VERIFIED';

-- name: GetRandomEnterpriseAgentAlias :one
SELECT a.agent_alias, d.domain_name
FROM toro_core.enterprise_agent_aliases a
JOIN toro_core.enterprise_domains d ON a.domain_id = d.id
WHERE d.status = 'VERIFIED'
ORDER BY random()
LIMIT 1;
