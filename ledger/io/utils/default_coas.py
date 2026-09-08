categories = {
    "assets": {
        "root_role": "root_assets",
        "accounts": [
            { "name": "Central cash", "role": "asset_ca_cash", "balance_type": "debit", "code": "51611" },
            { "name": "Checking account", "role": "asset_ca_cash", "balance_type": "debit", "code": "5141" },
            { "name": "Savings account", "role": "asset_ca_cash", "balance_type": "debit", "code": "5142" },
            { "name": "Accounts receivable", "role": "asset_ca_recv", "balance_type": "debit", "code": "34211" },
            { "name": "Inventory", "role": "asset_ca_inv", "balance_type": "debit", "code": "3111" },
            { "name": "Prepaid expenses", "role": "asset_ca_prepaid", "balance_type": "debit", "code": "3491" },
            { "name": "Equipment", "role": "asset_ppe_equip", "balance_type": "debit", "code": "23321" },
            { "name": "Office furniture", "role": "asset_ppe_equip", "balance_type": "debit", "code": "2351" },
            { "name": "Office equipment", "role": "asset_ppe_equip", "balance_type": "debit", "code": "2352" },
            { "name": "Administrative and commercial buildings", "role": "asset_ppe_build", "balance_type": "debit", "code": "23214" },
            { "name": "Depreciation of buildings", "role": "asset_ppe_build_accum_depr", "balance_type": "credit", "code": "28321" }
        ]
    },
    "liabilities": {
        "root_role": "root_liabilities",
        "accounts": [
            { "name": "Accounts payable - category A", "role": "lia_cl_acc_payable", "balance_type": "credit", "code": "44111" },
            { "name": "Corporate Income Tax", "role": "lia_cl_taxes_payable", "balance_type": "credit", "code": "4453" },
            { "name": "VAT Collected", "role": "lia_cl_taxes_payable", "balance_type": "credit", "code": "4455" },
            { "name": "VAT Due (per Declaration)", "role": "lia_cl_taxes_payable", "balance_type": "credit", "code": "4456" },
            { "name": "Salaries and Wages Payable", "role": "lia_cl_wages_payable", "balance_type": "credit", "code": "4432" },
            { "name": "Social Security", "role": "lia_cl_other", "balance_type": "credit", "code": "4441" },
            { "name": "Bank Loans", "role": "lia_ltl_notes", "balance_type": "credit", "code": "1481" }
        ]
    },
    "equity": {
        "root_role": "root_capital",
        "accounts": [
            { "name": "Share capital", "role": "eq_capital", "balance_type": "credit", "code": "1111" },
            { "name": "Individual Capital (Sole Proprietorship)", "role": "eq_adjustment", "balance_type": "debit", "code": "11171" },
            { "name": "Owner's Drawing Account", "role": "eq_adjustment", "balance_type": "debit", "code": "11175" },
            { "name": "Retained Earnings (Credit)", "role": "eq_adjustment", "balance_type": "credit", "code": "1161" },
            { "name": "Retained Losses (Debit)", "role": "eq_adjustment", "balance_type": "credit", "code": "1169" },
            { "name": "Net Profit for the Year (Credit)", "role": "eq_adjustment", "balance_type": "credit", "code": "1191" },
            { "name": "Net Loss for the Year (Debit)", "role": "eq_adjustment", "balance_type": "credit", "code": "1199" }
        ]
    },
    "income": {
        "root_role": "root_income",
        "accounts": [
            { "name": "Domestic Sales of Merchandise", "role": "in_operational", "balance_type": "credit", "code": "7111" },
            { "name": "Export Sales of Merchandise", "role": "in_operational", "balance_type": "credit", "code": "7111" },
            { "name": "Sales of services", "role": "in_operational", "balance_type": "credit", "code": "71243" }
        ]
    },
    "expenses": {
        "root_role": "root_expenses",
        "accounts": [
            { "name": "Cost of goods sold", "role": "cogs_regular", "balance_type": "debit", "code": "6111" },
            { "name": "Purchases of Raw Materials", "role": "cogs_regular", "balance_type": "debit", "code": "61211" },
            { "name": "Salaries and Wages", "role": "ex_regular", "balance_type": "debit", "code": "61711" },
            { "name": "Owner's Salaries and Wages", "role": "ex_regular", "balance_type": "debit", "code": "61771" },
            { "name": "Social Charges on Owner's Remuneration", "role": "ex_regular", "balance_type": "debit", "code": "61774" },
            { "name": "Social Security Contributions", "role": "ex_regular", "balance_type": "debit", "code": "61741" },
            { "name": "Rent expense (Building)", "role": "ex_regular", "balance_type": "debit", "code": "61312" },
            { "name": "Utilities expense", "role": "ex_regular", "balance_type": "debit", "code": "61251" },
            { "name": "Supplies expense (Group A)", "role": "ex_regular", "balance_type": "debit", "code": "61221" },
            { "name": "Advertising expense", "role": "ex_regular", "balance_type": "debit", "code": "61446" },
            { "name": "Operating Risk Insurance Premiums", "role": "ex_regular", "balance_type": "debit", "code": "61343" },
            { "name": "Bank fees", "role": "ex_regular", "balance_type": "debit", "code": "61473" },
            { "name": "Other Operating Expenses", "role": "ex_other", "balance_type": "debit", "code": "6188" }
        ]
    }
}