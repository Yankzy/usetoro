-- +goose Up
ALTER TABLE shadow_erp.customers
ADD COLUMN industry text,
ADD COLUMN industry_icon text,
ADD COLUMN customer_description text,
ADD COLUMN customer_url text;

-- +goose Down
ALTER TABLE shadow_erp.customers 
DROP COLUMN industry, 
DROP COLUMN industry_icon, 
DROP COLUMN customer_description, 
DROP COLUMN customer_url;
