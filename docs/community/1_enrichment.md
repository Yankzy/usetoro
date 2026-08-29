## Service 1 of 5: Morocco Business Enrichment Service

I think this should be treated as **infrastructure**, not as a bookkeeping feature.

### 1. The job

The service accepts ugly, incomplete Moroccan commercial data and returns a **canonical, machine-readable business event**.

It should answer four questions:

1. **What happened?**
2. **Who are the parties?**
3. **What does this raw Moroccan banking/business language actually mean?**
4. **What accounting, tax, or compliance context is objectively associated with it?**

No LLM reasoning in the core enrichment path.

---

## A. Inputs

V1 should accept three major object types:

### Bank transaction

```json
{
  "date": "2026-07-19",
  "description": "PRLV IAM CASA 2128743",
  "amount": -842.30,
  "currency": "MAD",
  "account": "..."
}
```

### Bank statement

```text
PDF / CSV / XLSX / bank export
```

The ingestion adapter converts it into individual transactions before enrichment.

### Invoice

```json
{
  "supplier_name": "AMAZON WEB SERVICES EMEA SARL",
  "invoice_number": "...",
  "subtotal": 100,
  "tax": 0,
  "currency": "EUR"
}
```

Later we expand to receipts, purchase orders, CNSS documents, payroll, etc.

---

# B. What comes back

A transaction such as:

```text
PRLV IAM CASA 2128743
```

could become:

```json
{
  "event_type": "bank_transaction",

  "transaction": {
    "direction": "debit",
    "amount": 842.30,
    "currency": "MAD",
    "date": "2026-07-19"
  },

  "counterparty": {
    "entity_id": "ma_ent_...",
    "canonical_name_fr": "Itissalat Al-Maghrib S.A.",
    "canonical_name_ar": "...",
    "commercial_name": "Maroc Telecom",
    "aliases": [
      "IAM",
      "Maroc Telecom",
      "Itissalat Al-Maghrib"
    ],
    "country": "MA",
    "entity_type": "company",
    "industry": "telecommunications"
  },

  "payment": {
    "instrument": "direct_debit",
    "bank_descriptor": "PRLV",
    "reference": "2128743"
  },

  "classification": {
    "merchant_category": "telecommunications",
    "likely_business_purpose": "telecommunications_service"
  },

  "morocco_context": {
    "domestic_counterparty": true,
    "cross_border_service": false
  },

  "provenance": {
    "entity_match": "alias_registry",
    "ruleset_version": "ma-enrichment-2026.08",
    "confidence": 1.0
  }
}
```

**Importantly, enrichment is not bookkeeping.**

It doesn't decide:

> Debit account 6145, credit account 5141.

That's downstream accounting intelligence.

It says:

> This cryptic string represents Maroc Telecom, a Moroccan telecommunications company, this was a direct debit, this is the normalized reference, and these are the known attributes of the event.

That makes it useful to **any** Moroccan fintech, ERP, accountant, expense product, bank, lender or business software developer.

---

# C. The real asset: Moroccan Entity Registry

This is probably the heart of the service.

Create a canonical ID for every business entity we know:

```text
ma_ent_01J...
```

Then store:

```text
Canonical legal name
Commercial name
Arabic name
French name
Known abbreviations
Historical names
ICE
IF
RC
Legal form
Country
City
Industry
Website/domain
Known bank descriptors
Known invoice descriptors
Known payment aliases
Known merchant aliases
Parent/subsidiary relationships
Government/public entity indicator
```

OMPIC already operates Morocco's electronic company-creation infrastructure through DirectEntreprise and works with the relevant public administrations, which makes official company identity data an obvious authoritative source wherever legally/API-accessible. ([Direct Entreprise Guichet][1])

But the **alias database** becomes our contribution.

For example:

```text
"AMZN AWS"
"AWS EMEA"
"AMAZON WEB SERV"
"AWS*EU"
```

can all ultimately resolve to:

```text
Amazon Web Services EMEA SARL
```

And:

```text
IAM
MAROC TELECOM
ITISSALAT AL MAGHRIB
IAM CASA
اتصالات المغرب
```

can resolve to one canonical entity.

That database gets better continuously.

---

# D. Bank-specific enrichment

This is particularly valuable in Morocco because bank descriptions are terrible.

We create adapters/rulesets:

```text
attijariwafa/
banque_populaire/
bank_of_africa/
cih/
credit_agricole/
societe_generale/
...
```

Each knows how that institution encodes:

```text
VIR
VIR INST
PRLV
PRELEVEMENT
CB
CARTE
CHEQUE
CHQ
FRAIS
COM
VERSEMENT
REMISE
EFFET
VIREMENT RECU
VIREMENT EMIS
```

And more importantly, where inside the description the bank places:

```text
counterparty
reference
invoice number
card terminal
date
authorization number
transfer identifier
```

So:

```text
VIR RECU 00028392 STE ABC SARL FACT 1482
```

doesn't remain one opaque string.

It becomes fields.

---

# E. Deterministic entity resolution

This cannot merely be:

```python
if "IAM" in text:
    return "Maroc Telecom"
```

We need a proper resolution pipeline.

```text
Raw descriptor
      |
Normalization
      |
Exact identifier match
      |
Exact alias match
      |
Bank-specific descriptor rules
      |
Tokenized alias matching
      |
Domain / ICE / IF / RC evidence
      |
Weighted deterministic scorer
      |
Canonical entity
```

For example:

```text
ICE match                         +100
IF match                           +100
exact registered alias             +90
bank-specific known descriptor      +80
exact normalized legal name         +80
city agreement                      +15
known merchant pattern              +15
conflicting ICE                    -100
```

Then establish thresholds:

```text
>= 90    VERIFIED_MATCH
70-89    PROBABLE_MATCH
<70      UNRESOLVED
```

The service must be willing to say:

```json
{
  "counterparty": null,
  "resolution_status": "UNRESOLVED"
}
```

instead of hallucinating.

---

# F. Fiscal enrichment

This is where it becomes substantially more valuable than generic Plaid-style enrichment.

Consider:

```text
AWS
Google Cloud
Meta
Microsoft
Adobe
OpenAI
GitHub
Cloudflare
```

The enrichment service recognizes that the counterparty is foreign and that the transaction represents an imported service.

It can attach **facts and potentially applicable rules**:

```json
{
  "cross_border": true,
  "counterparty_country": "IE",
  "service_category": "cloud_computing",

  "fiscal_context": {
    "imported_service": true,
    "vat_review_required": true,
    "withholding_tax_review_required": true,

    "rules": [
      {
        "rule_id": "MA-TAX-...",
        "status": "potentially_applicable"
      }
    ]
  }
}
```

There needs to be a hard distinction between:

**Enrichment fact**

> Foreign supplier, software/cloud service, EUR payment.

and:

**Accounting/tax decision**

> Apply this exact tax treatment.

The first can safely be deterministic infrastructure.

The second can depend on company circumstances, treaties, tax status and current legislation and should be handled by the accounting/compliance layer.

That separation is extremely important.

---

# G. Rules become first-class objects

Rather than burying knowledge in application code:

```yaml
rule_id: ma.cross_border.service
version: 3

when:
  counterparty.country:
    not: MA
  transaction.category:
    in:
      - software
      - cloud_service
      - consulting
      - advertising

annotate:
  imported_service: true
  vat_review_required: true
  withholding_tax_review_required: true
```

Now the rule can be:

* versioned
* tested
* reviewed
* cited
* corrected
* contributed
* deprecated

This is where an open community becomes useful without everybody editing the same application.

---

# H. Provenance on absolutely everything

If we return:

```text
Maroc Telecom
```

the caller should be able to ask:

> Why?

And receive:

```json
{
  "value": "Maroc Telecom",
  "source": "entity_registry",
  "matched_alias": "IAM",
  "registry_record": "ma_ent_...",
  "ruleset": "iam-bank-aliases@17"
}
```

If we say:

```text
cross_border_service = true
```

we preserve why.

This makes it suitable for financial software because **every enrichment is auditable**.

---

# I. API surface

Keep it absurdly simple.

### Single transaction

```http
POST /v1/enrich/transaction
```

### Batch

```http
POST /v1/enrich/transactions
```

### Invoice

```http
POST /v1/enrich/invoice
```

### Entity resolution

```http
POST /v1/entities/resolve
```

### Entity lookup

```http
GET /v1/entities/{entity_id}
```

### Bank statement

```http
POST /v1/enrich/bank-statement
```

And a downloadable/self-hostable library for people who don't want to send financial data to Toro:

```text
Go
Python
JavaScript
REST API
```

That's important.

The **knowledge itself should be portable**.

---

# J. Community contribution model

This is where the Hugging Face analogy becomes relevant, but enrichment remains only one service in the larger commons.

Someone discovers that Banque Populaire represents Glovo settlements as:

```text
VIR GLOVOAPP23...
```

They contribute:

```yaml
aliases:
  - pattern: "^GLOVOAPP"
    entity: glovo_ma
```

Someone contributes 3,000 Moroccan company aliases.

Someone contributes a parser for Crédit du Maroc exports.

Someone contributes Arabic/French normalization.

Someone contributes Moroccan government entities.

Someone contributes merchant categories.

Every contribution gets automated tests before entering the canonical registry.

---

# K. Why a business uses this even if it never uses Toro bookkeeping

Imagine an expense-management startup.

Today they receive:

```text
CB 1907 GOOGLE*ADS 458392 IE
```

They need to build everything themselves.

With this service:

```text
POST /enrich/transaction
```

and receive:

```text
Google
Advertising
Ireland
Card payment
Foreign supplier
Imported digital service
Fiscal review annotations
Canonical entity ID
Normalized transaction
```

That's real infrastructure.

A lending company can use it.

A CFO dashboard can use it.

An ERP can use it.

A bank can use it.

An accountant can use it.

A fraud product can use it.

**None of them has to buy bookkeeping from us.**

---

# L. Economics

This is also why I like it as the first free service.

The expensive work happens **once**:

```text
building registries
building mappings
writing rules
writing parsers
maintaining datasets
```

Afterwards, a lookup is essentially:

```text
database lookup
+ string normalization
+ deterministic matching
+ rule evaluation
```

No reasoning-token bill per transaction.

At large scale we can even distribute the datasets/rules locally and reduce our own compute further.

And Toro's bookkeeping gets the same infrastructure for free internally.

So this is not charity.

**We are externalizing infrastructure we already need and allowing the rest of Morocco to make it better.**

That is Service #1.
