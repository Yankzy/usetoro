-- name: CreateLeadForm :one
INSERT INTO marketing.lead_forms (
    website,
    form_data
) VALUES (
    $1, $2
) RETURNING *;
