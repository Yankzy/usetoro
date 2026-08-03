# Technical PRD: Toro Pre-Sage Email MVP (`pre-sage-mvp`)

## Status

- [x] Draft
- [ ] Review
- [ ] Approved
- [ ] Executed

**Document Version:** 1.0  
**Target Architecture:** Golang 1.22+, Postmark Inbound/Outbound Webhooks, NATS JetStream, PostgreSQL/AlloyDB (`fignode` schema)  
**Core Objective:** Deploy the **Pre-Sage Email MVP**, which encompasses **100% of Toro Cloud's AI engine capabilities** (ASE DAG execution, multi-document 4-way matching, `fignode` multi-tenant store, stateful task hydration, and interactive HITL chasers) **without requiring the local desktop connector agent**. 

Accountants interact via email (`rap_cleint_number@a.usetoro.io`), and Toro Cloud delivers pixel-perfect, balanced **Sage `.PNM` / `.CSV` import files** matching the client's custom Sage import template spec directly to their inbox.

---

## 1. Pre-MVP Scope: Cloud Core vs. Stage 1 Desktop App

| Capability | **Pre-Sage Email MVP (Current Scope)** | **Phase 1 Desktop App (`toro-sage-connector`)** |
| :--- | :--- | :--- |
| **ASE DAG Matching (`pcm_bank_reconciliation.yml`)** | ✅ Full Cloud Execution | ✅ Full Cloud Execution |
| **Multi-Document 4-Way Match (BC/BL/Facture/Bank)** | ✅ Included | ✅ Included |
| **Stateful Task Hydration & Persistence** | ✅ Included (`fignode` DB) | ✅ Included (`fignode` DB) |
| **Multi-Tenant Client Dossier Store** | ✅ Included (`reconciliation+dossier@...`) | ✅ Automated via Local Schema Sync |
| **Interactive Discrepancy Chasers (HITL)** | ✅ Email / WhatsApp Reply | ✅ Desktop UI / Email |
| **Sage Import Payload Generation** | ✅ Tailored `.PNM` / `.CSV` Email Attachment | ✅ Direct SQL / SBO Local Injection + `.PNM` |
| **On-Premise Desktop Daemon** | ❌ None (Pure Cloud) | ✅ `toro-sage-agent.exe` Daemon |

---

## 1. System Architecture & Component Mapping

```
 ┌─────────────────────────────────────────────────────────────┐
 │ 1. Accountant Emails Documents to rap_cleint_number@a.usetoro.io │
 │    (Bank Statement PDF, Invoices, BL scans)                 │
 └──────────────────────────────┬──────────────────────────────┘
                                │
                                ▼
 ┌─────────────────────────────────────────────────────────────┐
 │ 2. PostmarkInboundEmailWorker (EXISTS)                      │
 │    file:///Users/Yankz/programming/usetoro/go/internal/   │
 │    workers/postmark_inbound_email.go                        │
 │    • Extracts S3 attachments & resolves email threads       │
 │    • Routes to NATS subject: 'worker.inbox.pcm_ocr'         │
 └──────────────────────────────┬──────────────────────────────┘
                                │
                                ▼
 ┌─────────────────────────────────────────────────────────────┐
 │ 3. PcmOcrWorker (EXISTS - UPGRADE REQUIRED)                 │
 │    file:///Users/Yankz/programming/usetoro/go/internal/   │
 │    workers/pcm_ocr_worker.go                                │
 │    • Standardizes Subscriptions() with BuildWorkerInbox...  │
 │    • Parses OCR documents & publishes to 'worker.inbox.ase_bridge'
 └──────────────────────────────┬──────────────────────────────┘
                                │
                                ▼
 ┌─────────────────────────────────────────────────────────────┐
 │ 4. Autonomous Semantic Engine (ASE) DAG Execution           │
 │    file:///Users/Yankz/programming/usetoro/go/internal/   │
 │    erp/ase/dags/pcm_bank_reconciliation.yml               │
 │    • Executed by dag.go & pcm_bank_reconciliation_tools.go  │
 └──────────────────────────────┬──────────────────────────────┘
                                │
                 Is Match Complete (C >= 0.98)?
                     /                     \
               YES  /                       \  NO (Missing BL/Facture)
                   /                         \
                  ▼                           ▼
┌─────────────────────────────┐   ┌─────────────────────────────┐
│ 5a. Sage .PNM Generator     │   │ 5b. Postmark Chaser Email   │
│     & Postmark Email Reply  │   │     "Missing BL for Line X. │
│     (Attaches .PNM file!)   │   │      Reply to this email."  │
└─────────────────────────────┘   └─────────────────────────────┘
```

---

## 2. Existing Foundation in Codebase

The Pre-MVP leverages existing, production-proven workers and engines already in the repository:

1. **`PostmarkInboundEmailWorker`** ([postmark_inbound_email.go](file:///Users/Yankz/programming/usetoro/go/internal/workers/postmark_inbound_email.go#L169-L180)):
   - Parses Postmark's JSON webhook payloads.
   - Uploads attachments to S3 via `infra.S3Service`.
   - Parses recipient alias (`rap_client_number@...`) and routes directly to `worker.inbox.pcm_ocr`.
   - Performs email thread resolution via `In-Reply-To` headers.

2. **`PcmOcrWorker`** ([pcm_ocr_worker.go](file:///Users/Yankz/programming/usetoro/go/internal/workers/pcm_ocr_worker.go)):
   - Consumes messages from `worker.inbox.pcm_ocr`.
   - Resolves tenant entity ID via `w.db.GetEntityIDByEmail`.
   - Builds task envelope and publishes to `worker.inbox.ase_bridge`.

3. **Reconciliation DAG & Tools**:
   - DAG Topology: [pcm_bank_reconciliation.yml](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/dags/pcm_bank_reconciliation.yml).
   - Domain Tools: [pcm_bank_reconciliation_tools.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/domain_tools/pcm/pcm_bank_reconciliation_tools.go) and [pcm_bank_reconciliation_store.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/domain_tools/pcm/pcm_bank_reconciliation_store.go).
   - Moroccan Accounting Knowledge: [chart_of_accounts_morocco.json](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/chart_of_accounts_morocco.json) and [plan_comptable_explainations.json](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/plan_comptable_explainations.json).

4. **Postmark Outbound Tracking**:
   - `PostmarkOutboundEventsWorker` ([postmark_outbound_events.go](file:///Users/Yankz/programming/usetoro/go/internal/workers/postmark_outbound_events.go)).

---

## 3. Required Upgrades & New Components

### Operational Realities & Real-World Guardrails

To prevent email-based automation failures in real-life accounting practice (*Fiduciaires* managing 100+ client companies), the Pre-MVP enforces **4 Operational Guardrails**:

1. **Client Dossier Address Tagging (`rap_cleint_number@a.usetoro.io`)**:
   - Accountants email a specific client alias (e.g. `rap_cleint_number@a.usetoro.io`) or include `[ATLAS_SARL]` in the Subject line.
   - `PostmarkInboundEmailWorker` ([postmark_inbound_email.go](file:///Users/Yankz/programming/usetoro/go/internal/workers/postmark_inbound_email.go#L380)) parses the sub-address/subject tag to resolve the exact client company record in `fignode.staging_sessions`.

2. **Per-Client Sage Import Template Profiles (`.EMA` / CSV Spec)**:
   - Onboarding accountants upload their firm's Sage Import Spec (`.EMA` template layout).
   - Toro's `.PNM` exporter (`go/internal/erp/sage/pnm/exporter.go`) formats output columns (`Journal;Date;CompteG;CompteA;Piece;Libelle;Debit;Credit`) matching that specific firm's Sage 100 template configuration.

3. **Pre-Loaded Sage Vendor Master (`F_COMPTET` Upload)**:
   - Client auxiliary supplier codes (`CT_Num` e.g. `44110042` or `IAM001`) and ICE tax IDs are pre-synced/uploaded into `fignode.canonical_vendors`.
   - Prevents Toro from writing invalid auxiliary codes into `.PNM` files that Sage import would reject.

4. **Interactive Email Discrepancy & Chaser Resolution**:
   - If a 15 MAD bank fee delta exists or a BL is missing, Toro sends an interactive email reply:
     > *"We matched 14 of 15 lines. For Line 15 (Payment to Supplier X), there is a 15 MAD bank fee discrepancy. Reply '1' to post 15 MAD to Account 6147, or attach the missing BL."*
   - Replying directly to the email invokes `DomainTool.ResumeAgent()`, attaching the reply context and completing the match.

---

### A. Upgrade `PcmOcrWorker` ([pcm_ocr_worker.go](file:///Users/Yankz/programming/usetoro/go/internal/workers/pcm_ocr_worker.go))

The existing `PcmOcrWorker` has a hardcoded `Subscriptions()` method:
```go
// CURRENT (Sub-optimal):
func (w *PcmOcrWorker) Subscriptions() []SubscriptionConfig {
    return []SubscriptionConfig{
        {
            Subject: "worker.inbox.pcm_ocr",
            Group:   "pcm-ocr-worker-group",
        },
    }
}
```

#### Required Upgrade:
Refactor `Subscriptions()` to align with `PostmarkInboundEmailWorker` pattern using dynamic activity derivation:

```go
// UPGRADED Pattern:
func (w *PcmOcrWorker) Subscriptions() []SubscriptionConfig {
    _, workerCfg := w.cfg.Workers.GetForWorker(w)
    activityType := workerCfg.ActivityType
    if activityType == "" {
        activityType = "workers.pcm_ocr"
    }

    subject := workerCfg.Subject
    if subject == "" {
        if derived, err := core.BuildWorkerInboxFromActivity(activityType); err == nil {
            subject = derived
        } else {
            subject = "worker.inbox.pcm_ocr"
        }
    }

    group := workerCfg.Group
    if group == "" {
        group = groupFromSubject(subject)
    }

    return []SubscriptionConfig{
        {
            Subject: subject,
            Group:   group,
            Options: []nats.SubOpt{
                nats.Durable(durableFromSubject(subject)),
                nats.DeliverAll(),
                nats.AckExplicit(),
            },
        },
    }
}
```

### B. Implement Sage `.PNM` Generator (`go/internal/erp/sage/pnm/exporter.go`)

Create a helper package `pnm` to convert 4-way matched records into a valid Sage Paramétrable `.PNM` text buffer:

```go
package pnm

import (
    "bytes"
    "fmt"
    "time"
)

type JournalEntryLine struct {
    JournalCode  string    // e.g. "ACH" or "BQ1"
    Date         time.Time // DDMMYY
    GeneralAcc   string    // e.g. "61440000" or "44110000"
    AuxAcc       string    // e.g. "IAM001" for suppliers
    PieceRef     string    // Facture or BL Number
    Libelle      string    // Description
    Debit        float64   // Amount
    Credit       float64   // Amount
}

// FormatPNM exports balanced entries in native Sage 100 ASCII format
func FormatPNM(lines []JournalEntryLine) []byte {
    var buf bytes.Buffer
    for _, l := range lines {
        // Positional or CSV Sage import line formatting
        buf.WriteString(fmt.Sprintf("%s;%s;%s;%s;%s;%s;%.2f;%.2f\r\n",
            l.JournalCode,
            l.Date.Format("020106"),
            l.GeneralAcc,
            l.AuxAcc,
            l.PieceRef,
            l.Libelle,
            l.Debit,
            l.Credit,
        ))
    }
    return buf.Bytes()
}
```

### C. Update Outbound Email Delivery in `pcm_bank_reconciliation_tools.go`

In [pcm_bank_reconciliation_tools.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/domain_tools/pcm/pcm_bank_reconciliation_tools.go):

1. **On Match Success ($C \ge 0.98$)**:
   - Generate `.PNM` buffer via `pnm.FormatPNM()`.
   - Base64 encode the `.PNM` file as attachment `import_sage.pnm`.
   - Call Postmark Outbound API to email the sender (`payload.From`):
     - **Subject**: `[Toro] Reconciled Sage Import File - Task #<SessionID>`
     - **Body**: Detailed summary of 4-way matches, HT/TVA/TTC breakdown, and TVA deductibility dates.
     - **Attachment**: `import_sage.pnm`.

2. **On Incomplete Match ($C < 0.98$)**:
   - Send email reply detailing missing documents (e.g., missing BL or Facture).
   - Set email headers `In-Reply-To` and `References` to maintain thread continuity.
   - When the user replies with a photo of the missing document, `PostmarkInboundEmailWorker` routes the email back to `ResumeAgent()`, attaching the document and resuming the DAG.

---

## 4. Required Database Schemas (`shadow_erp` Integration)

Since Sage is the client's ERP, **all ERP mirror tables, client dossiers, import templates, and reconciliation tasks belong 100% inside `shadow_erp`** ([002_shadow_erp.sql](file:///Users/Yankz/programming/usetoro/sql/schema/002_shadow_erp.sql)), keyed uniformly by `realm_id` (the ERP company dossier ID):

### A. Existing Tables Reused ([002_shadow_erp.sql](file:///Users/Yankz/programming/usetoro/sql/schema/002_shadow_erp.sql))

1. **`shadow_erp.accounts`** (Chart of Accounts & PCM Mapping):
   - Keyed by `realm_id` (Client Dossier / Company ID) and `erp_id` (Account number e.g. `61440000`, `34552000`, `51410001`).
   - Fields: `name`, `account_type` (`Expense`, `Revenue`, `Asset`, `Liability`), `account_sub_type`, `classification`, `fully_qualified_name`.

2. **`shadow_erp.vendors`** (Vendor Master & Auxiliary `CT_Num` Resolution):
   - Keyed by `realm_id` and `erp_id` (Auxiliary supplier code e.g. `44110042` or `IAM001`).
   - Fields: `display_name`, `last_known_account_id` (FK to `shadow_erp.accounts.id`), `ai_synonyms` (`JSONB` array of raw OCR aliases e.g. `["IAM CASABLANCA", "MAROC TELECOM"]`).

---

### B. Pre-MVP `shadow_erp` Extensions

```sql
-- 1. Client Dossiers Master (Solves Hole #1: Which client dossier is this?)
CREATE TABLE IF NOT EXISTS shadow_erp.client_dossiers (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    realm_id TEXT NOT NULL UNIQUE, -- Multi-tenant ERP Company ID joining shadow_erp tables
    fiduciaire_id UUID NOT NULL, -- Accounting firm tenant ID
    dossier_code VARCHAR(50) NOT NULL, -- e.g. "1042", "atlas_sarl" (used in email alias rap_1042@a.usetoro.io)
    company_name VARCHAR(255) NOT NULL,
    ice_number VARCHAR(15), -- Identifiant Commun de l'Entreprise (15 digits)
    sage_template_profile_id UUID, -- FK to shadow_erp.sage_import_templates
    event_source TEXT NOT NULL DEFAULT 'toro_internal',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    CONSTRAINT unique_dossier_per_fiduciaire UNIQUE (fiduciaire_id, dossier_code)
);

CREATE INDEX IF NOT EXISTS idx_shadow_erp_dossiers_lookup ON shadow_erp.client_dossiers (fiduciaire_id, dossier_code);
CREATE INDEX IF NOT EXISTS idx_shadow_erp_dossiers_realm  ON shadow_erp.client_dossiers (realm_id);

-- 2. Per-Client Sage Import Template Profiles (Solves Hole #2: Custom Sage .PNM formats)
CREATE TABLE IF NOT EXISTS shadow_erp.sage_import_templates (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    realm_id TEXT NOT NULL, -- Keyed to ERP Company ID
    template_name VARCHAR(100) NOT NULL, -- e.g. "Sage 100 Coala Standard", "Custom PNM"
    delimiter VARCHAR(5) NOT NULL DEFAULT ';',
    date_format VARCHAR(20) NOT NULL DEFAULT '020106', -- DDMMYY
    column_mapping JSONB NOT NULL, 
    -- JSONB Structure: 
    -- { "columns": ["journal_code", "date", "general_account", "auxiliary_account", "piece_ref", "libelle", "debit", "credit"], "has_header": false }
    event_source TEXT NOT NULL DEFAULT 'toro_internal',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_shadow_erp_sage_templates_realm ON shadow_erp.sage_import_templates (realm_id);

-- 3. Stateful Reconciliation Tasks (Solves Hole #4 & #5: Stateful Email Threads & HITL)
CREATE TABLE IF NOT EXISTS shadow_erp.reconciliation_tasks (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    realm_id TEXT NOT NULL REFERENCES shadow_erp.client_dossiers(realm_id) ON DELETE CASCADE,
    period_label VARCHAR(50) NOT NULL, -- e.g. "2026-07"
    status VARCHAR(50) NOT NULL DEFAULT 'OPEN', -- 'OPEN', 'HOLD_MISSING_DOCS', 'HOLD_DISCREPANCY', 'CLOSED'
    email_thread_id VARCHAR(255), -- Postmark Message-ID / In-Reply-To header
    missing_docs_summary JSONB, -- List of missing BLs / Factures for interactive reply
    discrepancies JSONB, -- List of fee deltas (e.g. 15 MAD bank fee)
    event_source TEXT NOT NULL DEFAULT 'toro_internal',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE INDEX IF NOT EXISTS idx_shadow_erp_rec_tasks_realm  ON shadow_erp.reconciliation_tasks (realm_id);
CREATE INDEX IF NOT EXISTS idx_shadow_erp_rec_tasks_status ON shadow_erp.reconciliation_tasks (realm_id, status);
```

---

## 5. Execution Sequence Diagram

```
Accountant           Postmark Inbound        PcmOcrWorker          ASE DAG           Sage Exporter        Postmark Outbound
    │                       │                      │                  │                    │                      │
    │ ─── Email Docs ─────► │                      │                  │                    │                      │
    │     rap_client_number@   │                      │                  │                    │                      │
    │                       │ ── Publish NATS ───► │                  │                    │                      │
    │                       │    pcm_ocr           │                  │                    │                      │
    │                       │                      │ ── Run DAG ────► │                    │                      │
    │                       │                      │    pcm_bank_rec  │                    │                      │
    │                       │                      │                  │ ── 4-Way Match ──► │                      │
    │                       │                      │                  │    (C >= 0.98)     │                      │
    │                       │                      │                  │                    │ ── Generate .PNM ──► │
    │                       │                      │                  │                    │                      │ ── Send Email ──► Accountant
    │                       │                      │                  │                    │                      │    (with .PNM)
```

---

## 5. Summary Checklist for Pre-MVP Implementation

- [ ] **Upgrade `PcmOcrWorker`** ([pcm_ocr_worker.go](file:///Users/Yankz/programming/usetoro/go/internal/workers/pcm_ocr_worker.go)): Use `core.BuildWorkerInboxFromActivity()` in `Subscriptions()`.
- [ ] **Implement `pnm/exporter.go`**: Build native Sage `.PNM` / `.CSV` formatter for balanced PCM journal entries.
- [ ] **Connect Outbound Email in Domain Tool** ([pcm_bank_reconciliation_tools.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/domain_tools/pcm/pcm_bank_reconciliation_tools.go)): Attach `.PNM` file on success and send Postmark reply.
- [ ] **Test Thread Resumption**: Verify that replying to a missing document chaser email triggers `ResumeAgent()` and updates the state.
