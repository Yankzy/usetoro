-- name: GetBankAccounts :many
SELECT name FROM shadow_erp.accounts 
WHERE active = true AND account_type IN ('Bank', 'Credit Card');

-- name: GetCreditCardAccounts :many
SELECT name FROM shadow_erp.accounts 
WHERE active = true AND account_type IN ('Credit Card');

-- name: GetCheckingAccounts :many
SELECT name FROM shadow_erp.accounts 
WHERE active = true AND account_type IN ('Bank');
