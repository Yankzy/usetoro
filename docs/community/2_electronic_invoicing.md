# Service 2 of 5: Free Moroccan Electronic Invoicing Infrastructure

This one is stronger than the enrichment service in one particular way: **businesses will eventually have no choice but to solve it.**

The legal foundation already exists. Article 145-IX of the Moroccan CGI requires affected taxpayers to have a computerized invoicing system meeting technical criteria set by the administration. What is still not fully settled publicly, as of August 24, 2026, is the final implementing decree and definitive technical specification. ([Factureo][1])

So I would build this as a **versioned public infrastructure layer**, ready to bind to the official DGI protocol once the specification is final.

## A. The basic promise

A Moroccan business should be able to do this:

```http
POST /v1/invoices
```

```json
{
  "seller": {
    "ice": "001234567890123"
  },
  "buyer": {
    "ice": "009876543210987"
  },
  "invoice_number": "FAC-2026-00421",
  "issue_date": "2026-08-24",
  "currency": "MAD",
  "lines": [
    {
      "description": "Maintenance informatique",
      "quantity": 1,
      "unit_price": 10000,
      "vat_rate": 20
    }
  ]
}
```

And our service handles all the ugly Moroccan fiscal plumbing.

The business should not need to understand XML schemas, electronic-signature envelopes, DGI error codes, protocol changes, or serialization rules.

Its mental model is simply:

> **Give us a valid commercial invoice. We turn it into a DGI-compliant electronic invoice.**

---

# B. There should actually be two products

This is important.

### 1. Free invoicing application

For a small Moroccan business that has no ERP.

They go to something like:

```text
invoice.toro.ma
```

Create their company once:

```text
Legal name
ICE
IF
RC
Address
VAT status
Bank details
Logo
```

Then create invoices through a very simple interface.

They enter:

```text
Customer
Items/service
Quantity
Price
VAT
```

We produce:

```text
Human-readable invoice
+
Structured electronic invoice
+
DGI submission/status
```

No subscription.

No "5 invoices free, then pay."

If we're serious about creating commons, **basic compliant invoicing remains free.**

---

### 2. Free developer API

This is arguably even more important.

Sage integrators, custom ERP developers, CRM companies, e-commerce systems, vertical SaaS products and internal corporate software should be able to use:

```http
POST /invoices
```

instead of individually implementing the DGI standard.

We become the compatibility layer.

---

# C. Canonical Moroccan Invoice Schema

Do **not** make the DGI's eventual raw schema our public API.

That's a mistake.

Create our own stable canonical representation:

```json
{
  "schema": "ma.invoice.v1",

  "invoice": {
    "type": "invoice",
    "number": "FAC-2026-00421",
    "issue_date": "2026-08-24",
    "currency": "MAD"
  },

  "seller": {
    "entity_id": "ma_ent_123",
    "ice": "...",
    "if": "...",
    "legal_name": "..."
  },

  "buyer": {
    "entity_id": "ma_ent_456",
    "ice": "...",
    "legal_name": "..."
  },

  "lines": [...],

  "totals": {
    "net": 10000,
    "vat": 2000,
    "gross": 12000
  }
}
```

Then internally:

```text
ma.invoice.v1
      |
      +-- DGI profile 2026.1
      |
      +-- DGI profile 2027.1
      |
      +-- PDF
      |
      +-- Sage
      |
      +-- accounting software
```

That protects everybody using us from regulatory changes.

If tomorrow DGI changes field `X` or replaces one XML profile with another, businesses shouldn't rewrite their ERP.

**We rewrite one adapter.**

That's infrastructure.

---

# D. Validation engine

Before anything reaches DGI, we validate it ourselves.

For example:

```text
Seller ICE structurally valid?
Buyer ICE required?
Invoice number present?
Invoice number duplicate?
Issue date valid?
Currency valid?
Line totals mathematically correct?
VAT rates valid?
HT + TVA = TTC?
Required seller information present?
Required buyer information present?
Credit note references original invoice?
```

Output:

```json
{
  "valid": false,
  "errors": [
    {
      "code": "MA_INV_014",
      "field": "buyer.ice",
      "message_fr": "ICE du client requis pour cette opération.",
      "message_ar": "...",
      "severity": "ERROR"
    }
  ]
}
```

No DGI submission until deterministic validation succeeds.

---

# E. Fiscal calculation

The service should also be able to calculate invoice arithmetic deterministically.

Input:

```json
{
  "quantity": 3,
  "unit_price": 100,
  "vat_rate": 20
}
```

Output:

```json
{
  "net": 300,
  "vat": 60,
  "gross": 360
}
```

But this is important:

**the engine should not decide whether 20% is legally the correct VAT rate unless the caller asks another rules service to determine it.**

Electronic invoicing should enforce the supplied tax treatment and known structural rules.

It should not quietly become an AI tax adviser.

---

# F. DGI adapter

Once the official specification is available, create a dedicated implementation:

```text
dgi/
  schemas/
  serializer/
  signer/
  client/
  status/
  errors/
  webhooks/
```

Flow:

```text
Canonical Invoice
       |
       v
Local validation
       |
       v
DGI serializer
       |
       v
Electronic signature
       |
       v
DGI submission
       |
       v
DGI response
       |
       +-- accepted
       +-- rejected
       +-- pending
       +-- technical failure
```

Some current Moroccan vendors are already building products around structured invoice generation, signatures, API submission, webhooks and simulated DGI clearance, which confirms that this interoperability layer is becoming a commercial category. But several of those same vendors acknowledge that the final DGI decree/specification is still pending, so we should not encode vendor assumptions as law. ([Vouch][2])

---

# G. Normalize DGI errors

This could be surprisingly valuable.

Government APIs often return technical errors that mean nothing to normal businesses.

Suppose DGI eventually returns something equivalent to:

```text
ERR_3276_ICE_REF_INVALID
```

We return:

```json
{
  "status": "REJECTED",
  "error": {
    "code": "BUYER_IDENTITY_INVALID",
    "original_code": "ERR_3276_ICE_REF_INVALID",
    "message": "The buyer ICE could not be validated.",
    "field": "buyer.ice",
    "remediation": "Verify the customer's ICE and submit again."
  }
}
```

Now hundreds of software companies don't each have to maintain their own DGI error translation layer.

---

# H. Lifecycle, not just issuance

Invoices aren't static.

We need a proper state machine:

```text
DRAFT
  |
VALIDATED
  |
SIGNED
  |
SUBMITTED
  |
  +-- ACCEPTED
  |
  +-- REJECTED
  |
  +-- RETRYABLE_FAILURE
```

Then later:

```text
ACCEPTED
   |
   +-- CREDIT_NOTE_ISSUED
   +-- CANCELLED where legally permitted
   +-- PAID
   +-- PARTIALLY_PAID
```

That history must never be overwritten.

---

# I. Credit notes and corrections

This must be first-class.

If invoice:

```text
FAC-2026-421
```

is wrong, don't let users edit the already-issued document as though nothing happened.

Instead:

```text
FAC-2026-421
       |
       v
Credit note AV-2026-031
       |
       v
Replacement FAC-2026-422
```

The API should maintain those relationships.

```json
{
  "document_type": "credit_note",
  "references": {
    "original_invoice": "FAC-2026-421"
  }
}
```

That becomes valuable downstream for bookkeeping too.

---

# J. Receiving invoices

This is where things become very interesting for Toro.

Don't only solve:

> "How do I send an invoice?"

Solve:

> **"How does my business receive its electronic invoices?"**

Give every company an invoice inbox:

```text
/company/{ICE}/invoices/incoming
```

Incoming structured invoices are normalized into our canonical schema.

Now:

```text
Supplier
    |
    | electronic invoice
    v
DGI
    |
    v
Company invoice inbox
```

The company's ERP can subscribe:

```http
POST /webhooks
```

Then:

```json
{
  "event": "invoice.received",
  "invoice_id": "..."
}
```

This is enormously useful for accountants.

No PDF scraping.

No OCR.

No email attachment.

The invoice arrives as actual structured data.

---

# K. And now Service 1 plugs directly into Service 2

Incoming invoice:

```text
DGI electronic invoice
       |
       v
Canonical invoice
       |
       v
Enrichment service
       |
       +-- resolve supplier
       +-- canonical entity
       +-- industry
       +-- cross-border/domestic
       +-- known commercial context
       |
       v
Bookkeeping
```

This is where the commons begins to compound.

The two services are independent.

But together they're much more valuable.

---

# L. SDK and self-hosting

We should publish:

```text
ma-invoice-schema
ma-invoice-validator
ma-invoice-dgi-adapter
```

with SDKs:

```text
Go
Python
Node.js
Java
PHP
```

A company should be able to run the validator locally.

Sensitive commercial invoice content doesn't need to touch our servers just to determine whether it's structurally valid.

The hosted infrastructure becomes necessary mainly for things such as DGI connectivity, event delivery, managed credentials and convenience.

---

# M. Sandbox

Before companies dare send fiscal documents through us, developers need a sandbox.

Something like:

```text
sandbox.invoice.toro.ma
```

They submit invoices and receive realistic responses:

```json
{
  "status": "REJECTED",
  "errors": [...]
}
```

including test cases for:

```text
invalid ICE
bad invoice number
incorrect totals
unsupported tax rate
duplicate invoice
invalid signature
DGI unavailable
timeout
rejected credit note
```

When the official DGI sandbox/API exists, our sandbox mirrors its behavior.

This alone could make the infrastructure the easiest way to develop Moroccan e-invoicing integrations.

---

# N. Why businesses care

### Small business

They get compliant invoicing without buying another SaaS subscription.

### Accountant

They can manage standardized electronic invoices instead of hunting PDFs through email and WhatsApp.

### ERP company

They implement one API instead of becoming experts in DGI plumbing.

### Large company

They put an adapter between their internal systems and DGI rather than coupling everything directly to government infrastructure.

### Startup

They get Moroccan e-invoicing capability almost instantly.

### Software developer

They get schemas, validators, SDKs, sandbox and documentation for free.

---

# O. What is free and what isn't

This distinction matters if we don't want the commons to become an expensive charity project.

**Free forever:**

```text
Schemas
Validation rules
SDKs
DGI serialization
Basic API submission
Receiving invoices
Basic invoice UI
Sandbox
Open documentation
Standard webhooks
Basic entity integration
```

Potentially paid later:

```text
Huge-volume guaranteed infrastructure
Dedicated environments
Enterprise SLA
Custom integrations
Long-term managed archival
Advanced permissions
ERP migration
Accounting automation
Reconciliation
AI processing
Custom compliance workflows
Enterprise support
```

So we're not charging businesses a toll merely to comply with their government.

We charge when they ask us to **operate their business processes**.

That distinction feels very aligned with the larger idea.

---

# P. Strategic effect

This service gives us something the enrichment service alone doesn't:

**a standardized transaction boundary between Moroccan companies.**

Initially:

```text
Business A
   |
   v
Toro e-invoice infrastructure
   |
   v
DGI
   |
   v
Business B
```

Eventually, when both businesses have agents:

```text
Business A Agent
       |
       | invoice
       v
Business B Agent
```

The invoice schema, company identities and delivery infrastructure are already there.

We haven't forced A2A adoption.

We've spent years making the **rails underneath A2A normal infrastructure**.

That makes electronic invoicing a very strong Service #2. ([Factureo][1])

[1]: https://www.factureo.ma/blog/article-145-ix-cgi-facturation-electronique-maroc/?utm_source=chatgpt.com "Article 145-IX CGI et facturation électronique au Maroc : ce qu'il impose — Factureo"
[2]: https://www.vouch.ma/ar?utm_source=chatgpt.com "Vouch — الفوترة الإلكترونية وفقًا للمادة 145-IX من المدونة العامة للضرائب"
