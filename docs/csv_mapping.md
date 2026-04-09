# CSV Mapping Flow

This document outlines the end-to-end architecture and data flow for the CSV mapping process, detailing how raw CSV/Excel bank statements uploaded by users are processed, mapped, and semantically enriched by the TAP (Toro Agent Protocol) Agent Network.

## High-Level Architecture

The process relies on an event-driven, choreography-based microservice architecture over NATS JetStream, dividing the work between the Gate API, TAP AI Agents, and backend worker services.

> [!NOTE]
> **TAP Pipeline Pattern**  
> This workflow deliberately implements a **Pipeline Pattern**. In the TAP ecosystem, while agents often negotiate work via a `CFP` -> `PROPOSE` lifecycle, they can also form a pipeline. In a pipeline, one agent receives a proposal, completes its work, and emits an `INFORM` proof. Subsequent agents listen directly to this proof, chaining their execution to the previous agent's output without requiring a new `CFP`.

1. **Gate API**: Handles file ingestion and initiates the workflow via a `CFP`.
2. **TAP CSV Mapping Agent**: Uses LLMs to infer the structure of the CSV and standardize column names.
3. **CSV Mapping Worker**: Persists the standardized raw rows into the staging database.
4. **TAP Enrichment Agent**: Performs semantic analysis for vendor matching, Chart of Accounts mapping, and deduping.

---

## Detailed Step-by-Step Flow

### 1. File Ingestion & Delegation (`Gate API`)
**File:** `go/internal/api/upload_handler.go`

- **Endpoint:** `POST /files/upload`
- **Action:** A user uploads a messy CSV or XLSX bank statement.
- **Process:**
  1. The file is parsed and validated (max size 20MB, max 10,000 rows).
  2. A new Cleanup Session is created in the database via `CreateCleanupSession`.
  3. A `core.TaskDefinition` is constructed with domain `accounting.cleanup` and packaged into a `CFP` (Call For Proposals) envelope. The payload contains the session ID, realm ID, and all raw rows.
  4. The envelope is broadcasted to the NATS JetStream topic: `events.accounting.1.cleanup`.
  5. The API returns `202 Accepted` to the client, along with a JSON payload containing the `session_id` and `status` ("PROCESSING").

### 2. AI Column Mapping (`TAP CSV Mapping AI Agent`)
**File:** `tap/agents/csv_mapping/agent.go`

- **Initialization:** Registers its capabilities (`accounting.cleanup`) with the Almanac registry on startup.
- **Trigger:** Listens to `events.accounting.1.cleanup` CFPs on NATS JetStream.
- **Process:**
  1. Replies with a `PROPOSE` envelope (Gate currently assumes auto-acceptance).
  2. Extracts the first few rows (up to 20) of the payload and sends them to the LLM.
  3. The LLM acts as an expert data analyst and outputs a strict JSON mapping denoting the 0-based column indices for Date, Description, Amount, Vendor, and identifies the sign convention (`is_expense_positive`) or split Debit/Credit columns.
  4. The agent uses this mapping to parse the rest of the file, transforming the raw rows into a uniform `RawRow` schema.
  5. The agent wraps the standardized `RawRow`s into a `core.Proof` (Type: `core.ProofAPI` / `"proof.api"`) envelope (Performative: `INFORM`, target: `did:toro:hive`) and publishes it to the NATS JetStream topic `proof.accounting.cleanup.columns`.
  6. **Reliability (JetStream):** Operates on a durable consumer (`csv-mapping-agent-durable`) with `AckExplicit`. Transient failures trigger `msg.Nak()`, and poison pills (retries > 3) trigger `msg.Term()`.

### 3. Database Persistence (`CSV Mapping Worker`)
**File:** `go/internal/workers/csv_mapping_worker.go`

- **Trigger:** Listens to `proof.accounting.cleanup.columns` on NATS JetStream.
- **Process:**
  1. Unmarshals the `INFORM` envelope containing the `RawRow` structured data from the CSV Mapping Agent.
  2. Updates the cleanup session status in the database to `PROCESSING`.
  3. Iterates over the standardized rows and inserts them into the database using `InsertCleanupRow`. 
  4. At this point, the raw structural parsing is complete, and the data is securely staged in Postgres.
  5. **Reliability (JetStream):** Operates on a durable consumer (`csv-mapping-worker-durable-v2`) with `AckExplicit`. Transients trigger `msg.Nak()`, while poison pills (retries > 3) update the session status to `ERROR` before calling `msg.Term()`.

### 4. Semantic AI Enrichment (`TAP Enrichment AI Agent`)
**File:** `tap/agents/enrichment/agent.go`

- **Initialization:** Registers its capabilities (`accounting.enrichment`) with the Almanac registry on startup.
- **Trigger:** Listens directly to the `proof.accounting.cleanup.columns` (`INFORM` proof) on NATS JetStream. *(Pipeline execution: it is informed by the CSV Mapping Agent's completion rather than responding to a new `CFP`.)*
- **Process:**
  1. **Sync Wait:** Polls the database (`GetPendingSessionRows`) for up to 10 seconds to ensure the `CSV Mapping Worker` has finished inserting the staged rows.
  2. Updates the cleanup session status to `ENRICHING`.
  3. **Concurrent Processing:** Processes rows in parallel with a concurrency limit of 5. For each row, the agent leverages the embedded `ai.EntityResolver`:
     - **Layer 1 (Entity Resolution):** Matches the transaction description against known internal vendors to predict the `PredictedVendorID` and `NormalizedVendor`.
     - **Layer 2 (Chart of Accounts):** Predicts the standard GL account based on the transaction type and vendor, outputting `PredictedAccountID` and `AIReasoning`.
  4. Calculates an overall `ConfidenceScore` for the predictions.
  5. **Post-Processing:** Runs deduplication (`cleanup.Deduplicator`) and recurring transaction analysis heuristics across the enriched row set.
  6. **Persistence:** Batch updates the rows in the database via `UpdateRowEnrichment`.
  7. Updates the session status to `ENRICHED`.
  8. Broadcasts a final completion event `proof.accounting.cleanup.enrichment` to notify the system that the workflow logic is complete.
  9. **Reliability (JetStream):** Operates on a durable consumer (`enrichment-agent-durable`) with `AckExplicit`. Transients trigger `msg.Nak()`, while poison pills (retries > 3) update the session status to `ERROR` and call `msg.Term()`.
