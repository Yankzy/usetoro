-- +goose Up
ALTER TABLE shadow_erp.company_info
ADD COLUMN industry text,
ADD COLUMN industry_icon text,
ADD COLUMN business_model text,
ADD COLUMN mindset_hint text;

ALTER TABLE shadow_erp.vendors
ADD COLUMN industry text,
ADD COLUMN industry_icon text,
ADD COLUMN vendor_description text,
ADD COLUMN vendor_url text;

-- +goose Down
ALTER TABLE shadow_erp.company_info DROP COLUMN industry, DROP COLUMN industry_icon, DROP COLUMN business_model, DROP COLUMN mindset_hint;
ALTER TABLE shadow_erp.vendors DROP COLUMN industry, DROP COLUMN industry_icon, DROP COLUMN vendor_description, DROP COLUMN vendor_url;
