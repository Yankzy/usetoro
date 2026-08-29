# Service 5 of 5: Free Annual Financial Statements & Legal Filing Infrastructure

This one also qualifies because it is tied directly to statutory company obligations.

Moroccan companies must prepare annual financial statements, and for company forms such as SAs, the approved financial statements must be filed with the commercial court registry. The Ministry of Finance specifically notes that under Law 17-95, SAs must file two copies of their summary statements with the court registry within 30 days after approval by the general meeting. ([Finances Maroc][1])

The broader commercial framework also treats accounting records as formal legal documents that can be required in judicial proceedings and must be retained according to statutory rules. ([Adala][2])

So Service #5 should be:

> **Give us your finalized general ledger and company metadata. We generate the legally required Moroccan annual accounts, validate them, assemble the filing package, and prepare or submit the legal deposit.**

## A. Input

The business or accountant provides:

```json
{
  "company": {
    "legal_form": "SARL",
    "ice": "...",
    "if": "...",
    "rc": "...",
    "fiscal_year_end": "2026-12-31"
  },
  "ledger": [...],
  "prior_year": {...}
}
```

Or simply uploads:

```text
Sage 100 export
CSV
Excel
ERP export
Trial balance
General ledger
```

The service maps that accounting data into the Moroccan CGNC/PCGE structure.

---

## B. Generate the Moroccan états de synthèse

The core output should be the statutory financial statements, not some management dashboard.

Depending on the applicable accounting regime:

```text
Bilan
Compte de Produits et Charges
État des Soldes de Gestion
Tableau de Financement
ETIC
```

The engine should produce the required presentation from the closing balances.

For example:

```text
General ledger
     |
     v
PCGE validation
     |
     v
Closing balance
     |
     +-- Bilan
     +-- CPC
     +-- ESG
     +-- Tableau de financement
     +-- ETIC
```

No LLM is needed for the arithmetic or mapping.

---

## C. Deterministic accounting validation

Before generating anything:

```text
Total debit = total credit
Assets = liabilities + equity
Opening balance agrees with prior closing balance
CPC result agrees with balance-sheet result
Fixed-asset movements reconcile
Depreciation movements reconcile
Tax accounts reconcile
Cash accounts reconcile
Required account classes are structurally valid
```

Output:

```json
{
  "status": "INVALID",
  "errors": [
    {
      "code": "MA_FS_021",
      "message": "Balance-sheet result does not equal CPC net result.",
      "difference": 1250.00
    }
  ]
}
```

This is valuable because the company finds problems **before** statutory accounts are approved or deposited.

---

## D. Statutory statement generator

The API might look like:

```http
POST /v1/annual-accounts
```

and return:

```json
{
  "year": 2026,
  "status": "VALID",
  "statements": {
    "balance_sheet": "...",
    "cpc": "...",
    "esg": "...",
    "cash_flow_statement": "...",
    "etic": "..."
  }
}
```

Outputs should be available as:

```text
Structured JSON
Official-layout PDF
Spreadsheet
Machine-readable filing format
```

The structured representation is important because other software can consume it.

---

## E. Legal-form-aware filing requirements

Not every company has exactly the same corporate-law obligations.

The rules engine therefore starts from:

```text
SA
SARL
SAS
SNC
etc.
```

and determines:

```text
Which statements are required?
Does a statutory auditor report apply?
Which approvals are required?
Which documents accompany the filing?
What is the statutory filing deadline?
```

For example, the Ministry of Finance explicitly describes the SA requirement to file summary statements and the statutory auditor's report following shareholder approval. ([Finances Maroc][1])

Each requirement is versioned by:

```text
legal_form
effective_date
legal_source
filing_rule
```

---

## F. Assemble the legal deposit package

The business shouldn't have to figure out which PDFs belong together.

We generate:

```text
Annual financial statements
+
Approval metadata
+
Auditor report where required
+
Corporate resolutions/minutes metadata where required
+
Filing forms
+
Company identifiers
```

into:

```json
{
  "filing_package": "READY",
  "company": "...",
  "financial_year": "2026",
  "documents": [...],
  "missing_documents": []
}
```

If something mandatory is missing:

```json
{
  "status": "BLOCKED",
  "missing_documents": [
    {
      "type": "AUDITOR_REPORT",
      "reason": "Required for this company configuration."
    }
  ]
}
```

Unlike bookkeeping V1, this service **should block filing if the statutory package is incomplete**.

---

## G. Filing/deposit adapter

Where electronic filing is supported:

```text
Annual accounts
      |
      v
Validation
      |
      v
Legal filing package
      |
      v
Commercial Registry / relevant portal
      |
      v
Deposit receipt
```

Where direct API filing is not available, produce the exact accepted digital package and guide the user through the final submission step.

The important thing is that the infrastructure owns:

```text
formatting
validation
document assembly
versioning
submission status
receipt preservation
```

not that we pretend an API exists where one does not.

---

## H. General-meeting timing

For corporate forms requiring shareholder approval of annual accounts, the service should calculate the relevant corporate deadline and filing deadline.

Example state:

```json
{
  "year_end": "2026-12-31",
  "accounts_status": "PREPARED",
  "approval_status": "PENDING",
  "legal_deposit_status": "NOT_YET_PERMITTED"
}
```

Then:

```text
Accounts closed
    |
    v
Accounts prepared
    |
    v
Corporate approval
    |
    v
Legal filing period begins
    |
    v
Deposit
```

For SAs, the Ministry of Finance notes that the general meeting must occur by the end of June and filing follows within 30 days of approval. ([Finances Maroc][1])

---

## I. Immutable annual record

Once deposited:

```text
FY2026
  |
  +-- source ledger fingerprint
  +-- final statements
  +-- approval version
  +-- auditor report
  +-- filing package
  +-- submission timestamp
  +-- official receipt
```

Nothing gets silently replaced.

If restated:

```text
FY2026 ORIGINAL
      |
      v
FY2026 RESTATEMENT 1
```

with complete lineage.

---

## J. Why businesses would use it

Because every incorporated company eventually encounters this boring chain:

```text
Close books
Prepare statutory accounts
Check them
Get approval
Assemble documents
Deposit them
Retain proof
```

Accountants repeatedly perform essentially the same mechanical work across hundreds of companies.

So the free utility removes another mandatory compliance toll.

---

# The five-service starting set

We now have a coherent first set:

| Service                               | Why it exists                                                               |
| ------------------------------------- | --------------------------------------------------------------------------- |
| **1. Business Enrichment**            | Required infrastructure for correctly interpreting business/accounting data |
| **2. DGI Electronic Invoicing**       | Statutory invoicing/compliance infrastructure                               |
| **3. CNSS Compliance**                | Mandatory employee registration, salary declarations and contributions      |
| **4. DGI Tax Declarations**           | Mandatory TVA, IS/IR and withholding filings                                |
| **5. Annual Accounts & Legal Filing** | Mandatory annual financial statements and corporate filing                  |

There is an important distinction: **#2 through #5 are directly statutory obligations. #1 is not itself legally mandated**, but it is the shared deterministic data infrastructure that makes the other four substantially easier to automate. That is probably the right exception to keep.

[1]: https://www.finances.gov.ma/fr/Pages/detail-actualite.aspx?fiche=1905&utm_source=chatgpt.com "Publication du nouveau décret fixant les formes de publication des comptes annuels des Etablissements Publics – MEF – Royaume du Maroc"
[2]: https://adala.justice.gov.ma/api/uploads/2024/03/01/Code%20de%20commerce_compressed-1709282723074.pdf?utm_source=chatgpt.com "Royaume Du Maroc    Ministère de la Justice    Direction de législation et des études"
