# Service 4 of 5: Free DGI Tax Declaration Infrastructure

This is legally required territory.

Since **January 1, 2017**, Moroccan businesses subject to IS, IR, and TVA obligations have generally been required to submit their tax declarations and payments electronically through the DGI's SIMPL services. The Ministry of Finance explicitly ties this to Articles 155 and 169 of the CGI. ([Finances Maroc][1])

The service should therefore be:

> **Give us your accounting-period tax data. We generate, validate, and prepare the legally required DGI declarations in the exact required format.**

## A. Start with TVA

This should be the first module because TVA produces recurring compliance work.

Depending on the taxpayer, TVA declarations are generally monthly or quarterly. The CGI sets monthly filing for certain taxpayers and quarterly filing for others. ([TGR][2])

Input:

```json
{
  "company": {
    "ice": "...",
    "if": "...",
    "tax_regime": "TVA"
  },
  "period": "2026-08",
  "sales": [...],
  "purchases": [...],
  "credit_notes": [...],
  "withholdings": [...]
}
```

Output:

```json
{
  "period": "2026-08",
  "output_vat": 145320.00,
  "deductible_vat": 98210.00,
  "vat_withheld": 4200.00,
  "net_vat_due": 42910.00,
  "status": "VALID",
  "submission_payload": "..."
}
```

The service deterministically computes the declaration from the supplied accounting facts.

## B. Build the TVA deduction schedule automatically

This is where accountants lose real time.

The DGI requires supplier ICE information in TVA deduction schedules submitted through SIMPL-TVA, and non-conforming schedules can be rejected. ([Finances Maroc][3])

So we take purchase records:

```text
Invoice
Supplier
ICE
Date
HT
TVA
TTC
Payment information
```

and automatically construct the required deduction statement.

This plugs directly into Service #1:

```text
Messy supplier
      |
Enrichment
      |
Canonical entity + ICE
      |
TVA declaration
```

That is exactly the kind of compounding infrastructure we want.

## C. TVA withholding rules

Morocco now also has mandatory TVA withholding rules for specified transactions.

For certain service transactions, qualifying businesses must withhold **75% or 100% of the TVA** depending on the circumstances, and the DGI requires associated reporting and online payment through SIMPL-TVA. ([Finances Maroc][4])

Our rules engine should therefore calculate:

```json
{
  "invoice_id": "...",
  "vat_amount": 2000,
  "withholding_applicable": true,
  "withholding_rate": 0.75,
  "vat_withheld": 1500,
  "supplier_payable": "...",
  "rule_reference": "CGI_ART_117_IV_V"
}
```

Again, deterministic and versioned.

## D. IS declarations

Then add corporate income tax.

Input:

```text
Accounting result
+
Fiscal adjustments
+
Non-deductible expenses
+
Tax deductions
+
Tax credits
+
Prior losses where applicable
```

The engine generates:

```text
Accounting profit
        |
Fiscal reintegrations
        |
Fiscal deductions
        |
Taxable result
        |
Applicable IS computation
        |
Credits / installments
        |
Final amount
```

It should produce both:

1. the calculated tax position;
2. the DGI submission-ready declaration.

The free service is not doing bookkeeping for the company. It assumes the accounting data exists and turns it into the required fiscal declaration.

## E. IR withholding declarations

Businesses also act as tax collectors in several contexts.

For example:

```text
Employee salaries
Professional payments
Certain third-party remuneration
Non-resident payments
Other withholding situations
```

Where the law requires withholding and reporting, the service should maintain the applicable rule sets and generate the associated declaration.

So:

```http
POST /v1/dgi/withholding/calculate
POST /v1/dgi/withholding/declaration
```

This becomes a common deterministic tax engine that payroll software, accounting software, and ERPs can all use.

## F. Filing calendar

Every company should have a machine-readable obligation calendar generated from its tax profile:

```json
{
  "company": "...",
  "obligations": [
    {
      "type": "TVA",
      "period": "2026-08",
      "filing_frequency": "MONTHLY",
      "status": "PENDING"
    },
    {
      "type": "IS",
      "period": "2026",
      "status": "NOT_DUE"
    }
  ]
}
```

This is not a generic reminder app.

The calendar derives strictly from statutory obligations applicable to the taxpayer.

## G. Pre-filing validation

Before anything reaches DGI:

```text
Missing ICE
Invalid IF
TVA totals inconsistent
Supplier deduction statement mismatch
Duplicate invoice
Invalid tax period
Withholding detail missing
Declared turnover differs from underlying sales
Incorrect arithmetic
Missing mandatory annex
```

Return:

```json
{
  "valid": false,
  "errors": [
    {
      "code": "MA_TVA_042",
      "field": "deductions[17].supplier_ice",
      "message": "Supplier ICE is required for the TVA deduction schedule."
    }
  ]
}
```

## H. DGI submission adapter

Same philosophy as electronic invoicing:

```text
Canonical Tax Declaration
          |
          v
Validation
          |
          v
DGI-specific serializer
          |
          v
SIMPL / DGI submission
          |
          v
Receipt / rejection
```

If DGI exposes direct machine submission interfaces for a declaration, we integrate them.

If not, we produce the exact required electronic format for upload through SIMPL.

The public API must stay stable even when DGI formats change.

## I. Immutable filing history

Never overwrite tax filings.

```text
TVA August 2026
     |
     +-- Original calculation
     +-- Submitted declaration
     +-- DGI receipt
     +-- Correction #1
     +-- DGI receipt
```

Each record contains:

```text
Ruleset version
Source-data fingerprint
Calculation
Submission payload
Timestamp
DGI acknowledgement
Correction lineage
```

This becomes extremely useful during tax audits.

## J. Free developer infrastructure

Expose:

```text
/v1/tax/tva/calculate
/v1/tax/tva/validate
/v1/tax/tva/declaration

/v1/tax/is/calculate
/v1/tax/is/declaration

/v1/tax/withholding/calculate
/v1/tax/withholding/declaration

/v1/tax/obligations
/v1/tax/rules/{date}
```

And open-source:

```text
ma-tax-rules
ma-tva-validator
ma-dgi-schemas
ma-dgi-client
```

Now Moroccan accounting software doesn't need 20 separate implementations of the CGI.

## K. Why this belongs in the commons

The business doesn't have a choice about whether to:

```text
declare TVA
declare IS/IR where applicable
perform required withholding
submit required supporting schedules
pay the resulting tax
```

The choice today is simply **how much software/accountant effort is required to comply**.

So we remove the infrastructure tax.

And there's another interesting consequence.

Services #1 through #4 now form a chain:

```text
Raw transaction
      |
      v
1. Enrichment
      |
      v
2. Electronic invoice
      |
      v
Accounting records
      |
      +------------------+
      |                  |
      v                  v
3. CNSS             4. DGI Tax
Declarations        Declarations
```

We're slowly creating an open **machine-readable compliance layer for operating a Moroccan company**.

For Service #5, we should remain equally strict: something businesses are legally obligated to produce or declare, not simply something that makes operations easier.

[1]: https://www.finances.gov.ma/fr/pages/detail-actualite.aspx?fiche=2291&utm_source=chatgpt.com "Entreprises- personnes physiques ou morales- : télédéclaration et télépaiement obligatoire à compter du 1er janvier 2017 – MEF – Royaume du Maroc"
[2]: https://www.tgr.gov.ma/wps/wcm/connect/9856b6bb-dee8-4578-bfe8-ef1521dcc80f/code%2Bimpots%2B2019.pdf?MOD=AJPERES&utm_source=chatgpt.com "CODE GÉNÉRAL DES IMPÔTS"
[3]: https://www.finances.gov.ma/fr/Pages/detail-actualite.aspx?fiche=2390&utm_source=chatgpt.com "Le téléservice SIMPL-TVA de la DGI n’accepte que les relevés de déduction comprenant la mention de l'ICE – MEF – Royaume du Maroc"
[4]: https://www.finances.gov.ma/fr/pages/detail-actualite.aspx?fiche=6947&utm_source=chatgpt.com "Retenue à la source sur les opérations effectuées par les personnes assujetties à la TVA à partir du 1er juillet 2024 – MEF – Royaume du Maroc"
