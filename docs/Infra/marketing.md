# PRD: Toro Marketing DAG - Day 1 & 2 (Ingestion & Validation)

## 1. Objective

To build a robust processing pipeline for Toro’s internal Go-To-Market (GTM) data pipeline using the Autonomous Semantic Engine (ASE) architecture. This pipeline leverages the open-source `mailscout` library to replace third-party APIs (like Prospeo or Clay).

The pipeline will not only generate potential emails but will fully utilize `mailscout` to normalize prospect names, detect catch-all domains, discover valid corporate emails via SMTP, and periodically verify existing databases.

## 2. Architecture Overview

The pipeline is modeled as an **ASE Directed Acyclic Graph (DAG)** (`ase_marketing.yml`). Orchestration is managed entirely by the Go ASE engine.

The Python worker microservice (`python-worker`) exposes multiple discrete action providers via NATS JetStream, each corresponding to a specific utility within `python-worker/app/marketing/permutation.py`.

### DAG Topology Model
```yaml
dag:
  entry_node: target_ingestion
  nodes:

    # ------------------------------------------------------------------
    # STEP 1: INGESTION NODE
    # ------------------------------------------------------------------
    target_ingestion:
      name: target_ingestion
      batch_flush_seconds: 5
      kind: holding_gate
      batch_size: 50
      edge_type: static
      execution_parameters:
        action_provider: "marketing_csv_ingestion"
      children:
        INGEST_SUCCESS: domain_analysis
        INGEST_FAILED: terminal_error

    # ------------------------------------------------------------------
    # STEP 2: PRE-PROCESSING (NORMALIZE & CATCHALL CHECK)
    # ------------------------------------------------------------------
    domain_analysis:
      name: domain_analysis
      batch_flush_seconds: 5
      kind: holding_gate
      batch_size: 50
      edge_type: static
      execution_parameters:
        action_provider: "marketing_domain_analysis"
      children:
        ANALYSIS_SUCCESS: email_discovery
        IS_CATCHALL: terminal_catchall_review
        ANALYSIS_FAILED: terminal_error

    # ------------------------------------------------------------------
    # STEP 3: DISCOVERY (SMTP VALIDATION)
    # ------------------------------------------------------------------
    email_discovery:
      name: email_discovery
      batch_flush_seconds: 5
      kind: holding_gate
      batch_size: 50
      edge_type: static
      execution_parameters:
        action_provider: "marketing_email_discovery"
      children:
        DISCOVERY_SUCCESS: ongoing_monitoring
        DISCOVERY_FAILED: terminal_error

    # ------------------------------------------------------------------
    # STEP 4: ONGOING MONITORING
    # ------------------------------------------------------------------
    ongoing_monitoring:
      name: ongoing_monitoring
      batch_flush_seconds: 86400 # Run daily
      kind: holding_gate
      batch_size: 50
      edge_type: static
      execution_parameters:
        action_provider: "marketing_deliverability_check"
      children:
        STILL_VALID: terminal_smtp_handoff
        BOUNCED: terminal_error

    # ------------------------------------------------------------------
    # TERMINAL NODES
    # ------------------------------------------------------------------
    terminal_smtp_handoff:
      name: terminal_smtp_handoff
      kind: terminal
      execution_parameters:
        close_status: "READY_FOR_OUTREACH"
        
    terminal_catchall_review:
      name: terminal_catchall_review
      kind: terminal
      execution_parameters:
        close_status: "MANUAL_REVIEW_REQUIRED"

    terminal_error:
      name: terminal_error
      kind: terminal
      execution_parameters:
        close_status: "ERROR"
```

## 3. Specifications & Requirements

### Node 1: Target Ingestion (Python Worker)
**Purpose:** Parse ICP data from `targets.csv`.
* **Execution Context:** `app/marketing/ingestion.py`
* **NATS Subject:** `worker.marketing.ingest`
* **Execution Logic:** Parse rows, sanitize domains, filter out empty rows, and return JSON payload arrays.

### Node 2: Domain Analysis & Normalization (Python Worker)
**Purpose:** Ensure names are email-friendly and filter out catch-all domains to prevent hard bounces.
* **Execution Context:** `app/marketing/permutation.py`
* **NATS Subject:** `worker.marketing.analyze`
* **Execution Logic:** 
  1. Calls `normalize_prospect_name()` to strip diacritics and special characters from the prospect's name.
  2. Calls `check_domain_catchall()` to check if the target company's domain accepts any prefix (e.g., `*@domain.com`).
  3. If catch-all, returns state `IS_CATCHALL` so the ASE engine can halt processing (SMTP checks are useless on catch-alls).

### Node 3: Email Discovery (Python Worker)
**Purpose:** Use MailScout to find the specific, deliverable business email.
* **Execution Context:** `app/marketing/permutation.py`
* **NATS Subject:** `worker.marketing.discover`
* **Execution Logic:**
  1. Utilizes `mailscout.Scout().find_valid_emails()` internally via the `generate_permutations` logic.
  2. If the domain is provided with no name, brute forces common prefixes.
  3. Returns the valid email back to the Go engine.

### Node 4: Ongoing Deliverability Check (Python Worker)
**Purpose:** Periodically verify that discovered emails remain active before launching campaigns.
* **Execution Context:** `app/marketing/permutation.py`
* **NATS Subject:** `worker.marketing.verify`
* **Execution Logic:**
  1. Uses the `check_email_deliverability()` utility wrapper around `mailscout.check_smtp()`.
  2. Pings the SMTP server for the specific address.
  3. Returns `STILL_VALID` or `BOUNCED` to the ASE engine.

### Advanced Strategy: Bulk Discovery Workflow
**Purpose:** For large, unstructured lead lists, the architecture supports a separate bulk processing flow.
* **Execution Context:** `app/marketing/permutation.py`
* **Execution Logic:** An alternative DAG can batch prospects (e.g., `batch_size: 500`) and invoke the `find_emails_bulk()` method. This bypasses individual NATS round-trips for maximum throughput on massive CSV files, returning an aggregated list of valid emails per domain.

## 4. Environment & Tooling

* **Language:** Go (ASE Core) & Python 3.10+ (`python-worker`)
* **Message Broker:** NATS JetStream for `core.Envelope` delivery.
* **Python Libraries:**
  * `mailscout>=0.1.1` (Critical: Outgoing port 25 must be open in the Docker environment/cloud provider).
  * `pandas` or Python's native `csv` module.

## 5. Success Criteria

1. **Full Utilization:** The ASE DAG leverages all MailScout utilities (Normalization, Catchall detection, SMTP discovery, Bulk processing).
2. **DAG Initialization:** The `ase_marketing` DAG transitions perfectly through analysis, discovery, and verification.
3. **Catchall Handling:** The system correctly pauses prospects belonging to catch-all domains to protect domain reputation.
4. **Handoff Readiness:** Only 100% SMTP-verified emails reach `terminal_smtp_handoff`.

