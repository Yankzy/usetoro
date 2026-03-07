-- +goose Up
CREATE TABLE fignode.fignode_industries (
    name VARCHAR PRIMARY KEY,
    icon_emoji VARCHAR NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO fignode.fignode_industries (name, icon_emoji) VALUES
('Food Delivery', '🍔'),
('Payment Processing', '💳'),
('D2C E-Commerce', '📦'),
('E-Commerce / Retail', '🛒'),
('Petroleum / Fuel', '⛽'),
('Construction & Trades', '🏗️'),
('Creative Software (SaaS)', '🎨'),
('Creative Agency', '💡'),
('Point of Sale', '🧾'),
('Medical Practice', '🏥'),
('Commercial Aviation', '✈️'),
('B2B Software (SaaS)', '💻'),
('Productivity Software (SaaS)', '☁️'),
('Office Retail', '📎'),
('Financial Software', '🏦'),
('Legal Services', '⚖️'),
('Digital Advertising', '📢'),
('Telecommunications', '📡'),
('Real Estate Agency', '🏠'),
('Co-Working & Office Space', '🏢'),
('Online Payments', '📱'),
('Payroll & Accounting SaaS', '💼'),
('Restaurant Group', '🍽️'),
('Fast Food / Restaurant', '🍗'),
('Big Box Retail', '🏪'),
('Property Management', '🏘️'),
('Banking', '🏧'),
('Property & Casualty Insurance', '🛡️'),
('Logistics & Freight', '🚚'),
('Plumbing & Trades', '🔧'),
('Retail', '🛒'),
('Food & Beverage', '☕');

-- +goose Down
DROP TABLE fignode.fignode_industries;
