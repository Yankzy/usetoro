# Product Requirements Document (PRD)

**Project:** Toro Logistics - Phase 1
**Module:** Compliance OS (Carrier Single-Player Wedge)
**Target Release:** 90-Day Engineering Sprint
**Status:** APPROVED FOR DEVELOPMENT

---

## 1. Executive Summary

**The Goal:** Solve the marketplace "Cold Start" problem by building a mandatory, high-utility backend system for independent truck drivers and small fleets.

**The Strategy:** We are not building a load board or a marketplace in Phase 1. We are building a "Compliance OS"—a regulatory state machine that automates FMCSA compliance, IFTA tax calculations, document extraction, and broker risk scoring. By making a carrier's legal and financial survival dependent on our free software, we aggregate the supply side of the logistics market. Once we own the supply, we will force the demand side (shippers/brokers) onto our settlement protocol in Phase 2.

---

## 2. Core Architectural Principles

- **Event-Driven:** Every change in compliance or financial status must emit an event (e.g., `InsuranceExpired`, `InvoiceMatched`) to our append-only event log (NATS JetStream).
- **Deterministic State Machine:** A carrier's compliance is not a UI toggle; it is a calculated state (`ACTIVE`, `WARNING`, `CRITICAL`) derived deterministically from underlying API data.
- **Zero Manual Entry:** We rely on ELD API integrations, FMCSA public APIs, Bank APIs, and OCR Vision AI. We do not trust manual human data entry.

---

## 3. Epic Breakdown & Operational Requirements

### Epic 1: Identity & FMCSA Sync Engine

**Objective:** Automate the tracking of a carrier's legal operating authority.

- **REQ 1.1 - DOT Ingestion:** User inputs their USDOT Number during onboarding.
- **REQ 1.2 - FMCSA API Integration:** System calls the FMCSA SAFER/Public API to retrieve: Legal Name, MC Number, Authority Status, Safety Rating, and OOS (Out of Service) percentage.
- **REQ 1.3 - The Nightly Cron:** A background worker must query the FMCSA API every 24 hours at 02:00 UTC for all active `CarrierProfile` records.
- **REQ 1.4 - State Mutation:** If FMCSA returns `Authority: Revoked` or `Inactive`, the system must immediately mutate the carrier's `compliance_state` to `CRITICAL` and emit an `AuthorityRevoked` event.

### Epic 2: IFTA (Fuel Tax) Automation Engine

**Objective:** Automate the quarterly International Fuel Tax Agreement filing, eliminating the need for CPAs and spreadsheets.

- **REQ 2.1 - ELD Telemetry Ingestion:** Integrate with major ELD providers (Samsara, Motive, KeepTruckin) via OAuth to ingest daily `TripSegment` data (GPS pings, state border crossings, total miles per state).
- **REQ 2.2 - Fuel Receipt OCR:** User uploads fuel receipts via mobile camera. Vision AI extracts: Date, Gallons, State, Total Price. Stored as `FuelPurchase`.
- **REQ 2.3 - State Tax Table Registry:** Maintain a versioned database table of IFTA tax rates for all 48 contiguous states + Canadian provinces. Must be updatable by admins quarterly.
- **REQ 2.4 - The Math Engine:** On demand, the system calculates: `(Total State Miles / Fleet Average MPG) - State Fuel Gallons Purchased = Taxable Gallons`. Multiply by State Tax Rate.
- **REQ 2.5 - PDF Generation:** Generate a mathematically perfectly formatted, printable IFTA PDF packet using a headless document generator, ready for state submission.

### Epic 3: Smart Document Vault & Extraction

**Objective:** Replace manual back-office data entry with Vision AI document extraction.

- **REQ 3.1 - Encrypted Storage:** All uploaded PDFs/Images (Rate Cons, BOLs, Invoices) are stored in encrypted S3 buckets.
- **REQ 3.2 - OCR Pipeline:** Upon upload, route document to Vision AI pipeline (e.g., Google Document AI / AWS Textract).
- **REQ 3.3 - Entity Extraction:** Algorithm must reliably extract: Broker Name, Load ID, Rate Amount ($), and Payment Terms (e.g., Net-30).
- **REQ 3.4 - Auto-Invoicing:** Upon upload of a signed BOL (Proof of Delivery), the system automatically drafts an Invoice using the extracted Rate Con data and emails it to the Broker's AP department.

### Epic 4: Financial Ledger & Broker Risk Index

**Objective:** Track payments and score legacy brokers to protect carriers from predatory non-payment.

- **REQ 4.1 - Bank Sync:** Integrate with Plaid (or similar Bank API) for read-only transaction syncing.
- **REQ 4.2 - Deterministic Reconciliation:** Run a matching algorithm: `Invoice.amount == BankTransaction.amount && BankTransaction.date >= Invoice.date`.
- **REQ 4.3 - DTP Calculation:** When matched, calculate `DaysToPay = BankTransaction.date - Invoice.date`.
- **REQ 4.4 - Broker Scoring Algorithm:** Calculate the global `RiskScore` (0–100) for every broker across the entire Toro network based on the formula:

  ```bash
  0.4*(NormalizedDaysToPay) + 0.3*(DisputeRate) + 0.2*(UnderpaymentRate) + 0.1*(DetentionNonPaymentRate)
  ```

  Expose this score to carriers before they accept loads.

### Epic 5: Insurance State Management

**Objective:** Prevent load rejections by monitoring insurance expiration.

- **REQ 5.1 - Policy Ingestion:** User inputs Insurance Provider, Policy Number, Coverage Amount, and Expiration Date.
- **REQ 5.2 - Warning State:** Background worker checks dates daily. If `Expiration_Date < 30 days`, change state to `WARNING` and trigger SMS/Push notification.
- **REQ 5.3 - Critical State:** If `Expiration_Date < 0 days` and no new policy is uploaded, change `compliance_state` to `CRITICAL`.

---

## 4. Database Schema (High-Level)

```sql
-- Core Identity
CREATE TABLE carrier_profiles (
    id UUID PRIMARY KEY,
    dot_number VARCHAR(15) UNIQUE NOT NULL,
    mc_number VARCHAR(15),
    legal_name VARCHAR(255),
    authority_status VARCHAR(50), -- ACTIVE, INACTIVE, REVOKED
    compliance_state VARCHAR(20) DEFAULT 'ACTIVE', -- ACTIVE, WARNING, CRITICAL
    safety_rating VARCHAR(50),
    last_verified_at TIMESTAMP
);

-- IFTA Engine
CREATE TABLE trip_segments (
    id UUID PRIMARY KEY,
    carrier_id UUID REFERENCES carrier_profiles(id),
    state_code VARCHAR(2),
    miles_driven DECIMAL(10,2),
    start_timestamp TIMESTAMP,
    end_timestamp TIMESTAMP
);

CREATE TABLE fuel_purchases (
    id UUID PRIMARY KEY,
    carrier_id UUID REFERENCES carrier_profiles(id),
    state_code VARCHAR(2),
    gallons DECIMAL(8,2),
    price_total DECIMAL(10,2),
    purchase_date TIMESTAMP,
    receipt_image_url TEXT
);

-- Broker Intelligence
CREATE TABLE broker_payment_records (
    id UUID PRIMARY KEY,
    broker_name VARCHAR(255) INDEX,
    load_amount DECIMAL(10,2),
    invoice_date TIMESTAMP,
    payment_date TIMESTAMP,
    days_to_pay INT,
    was_disputed BOOLEAN DEFAULT false
);
```

---

## 5. Non-Functional Requirements

- **Security:** All PII and financial data must be encrypted at rest (AES-256) and in transit (TLS 1.3).
- **Auditability:** Every mutation to `carrier_profiles.compliance_state` must be recorded in an immutable audit log table with a timestamp and the trigger source (e.g., FMCSA Cron, Insurance Cron).
- **Latency:** OCR extraction pipeline must return structured data to the UI within 8 seconds of PDF upload to maintain the "magic" UX feel.

---

## 6. Out of Scope for Phase 1

- Broker load matching (Load Board).
- Factoring or lending (Capital Deployment).
- Shipper API integrations.

Phase 1 is strictly limited to building the single-player compliance wedge to aggregate carrier supply.
