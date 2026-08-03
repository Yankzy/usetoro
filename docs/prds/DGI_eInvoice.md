# Refactored Technical PRD: Khetm (ختم)

**Go + NATS JetStream Architecture Edition**

---

## 1. Architecture Overview (Go + NATS JetStream)

By leveraging **Go** for low-latency concurrent processing and **NATS JetStream** for event persistence and stream processing, Khetm decouples synchronous client requests from the asynchronous government clearance pipeline.

This guarantees:

* **Sub-50ms API Acknowledgments:** The client receives an immediate `202 Accepted` status while the clearance pipeline executes asynchronously.
* **Resilience to Government Outages:** If the DGI API drops or experiences latency spikes, NATS JetStream queues events in a persistent, disk-backed stream and automatically retries with backoff strategies without dropping transactions or blocking the web app.
* **Built-in Deduplication:** Uses JetStream's native message deduplication (`Nats-Msg-Id`) based on `invoice_id` to prevent double-submitting invoices to DGI during network retries.

---

## 2. NATS JetStream Topology & Subjects

```
                               ┌──────────────────────────────────────────────────────────┐
                               │                    NATS JETSTREAM                        │
                               │               STREAM: INVOICES_PIPELINE                  │
                               └──────────────────────────┬───────────────────────────────┘
                                                          │
┌────────────────┐  HTTP POST  ┌────────────────┐         │ Publish
│ Client App /   ├────────────>│ Go API Gateway │─────────┼────────────────────────┐
│ Accounting Web │<────────────┤ (Fiber/Gin)    │         │                        │
└────────────────┘  202 Accepted└────────────────┘         │                        │
                                                          ▼                        ▼
                                              [invoices.v1.received]    [invoices.v1.failed]
                                                          │                        ▲
                                                          │ Consumer               │
                                                          ▼                        │ Error
                                              ┌────────────────────────┐           │
                                              │ Go Worker: Validator   ├───────────┤
                                              └───────────┬────────────┘           │
                                                          │ Publish                │
                                                          ▼                        │
                                              [invoices.v1.validated]              │
                                                          │                        │
                                                          │ Consumer               │
                                                          ▼                        │
                                              ┌────────────────────────┐           │
                                              │ Go Worker: XML + Signer├───────────┤
                                              └───────────┬────────────┘           │
                                                          │ Publish                │
                                                          ▼                        │
                                              [invoices.v1.signed]                 │
                                                          │                        │
                                                          │ Consumer               │
                                                          ▼                        │
                                              ┌────────────────────────┐           │
                                              │ Go Worker: DGI Gateway ├───────────┘
                                              └───────────┬────────────┘
                                                          │ Publish
                                                          ▼
                                              [invoices.v1.cleared]
                                                          │
                                         ┌────────────────┴────────────────┐
                                         ▼                                 ▼
                             ┌──────────────────────┐          ┌──────────────────────┐
                             │ Go Worker: PDF/QR    │          │ Go Worker: Ledger    │
                             │ Renderer & S3 Vault  │          │ & Accounting Sync    │
                             └──────────────────────┘          └──────────────────────┘

```

### Stream Definition: `INVOICES_PIPELINE`

* **Subjects Captured:** `invoices.v1.>`
* **Storage:** File-backed (persistent across pod restarts)
* **Retention Policy:** WorkQueue or Interest-based
* **Deduplication Window:** 24 hours (keyed by `seller_ice:invoice_number`)

### Subject Hierarchy

| Subject | Description | Payload |
| --- | --- | --- |
| `invoices.v1.received` | Initial invoice submission ingested from client. | Raw JSON invoice payload |
| `invoices.v1.validated` | Tax fields (ICE, IF, totals) validated against logic rules. | Validated JSON + Metadata |
| `invoices.v1.signed` | UBL 2.1 XML generated and signed with PKI certificate. | Signed UBL 2.1 XML + Signature Bytes |
| `invoices.v1.cleared` | DGI verified invoice and returned Fiscal UUID + QR payload. | Cleared Record + DGI Receipts |
| `invoices.v1.failed` | Failed validation, signature error, or permanent DGI rejection. | Error Context + Failed Payload |

---

## 3. Go Microservice Architecture & JetStream Consumers

Instead of a monolithic backend, the system runs lightweight Go worker pools subscribing to specific NATS subjects using **JetStream Durable Push/Pull Consumers**.

### 1. Ingestion Layer (Go HTTP Service)

* **Role:** High-speed REST/gRPC API.
* **Logic:** Accepts incoming invoice creation requests, checks authorization, assigns a unique tracking UUID, publishes a message to `invoices.v1.received` with a `Nats-Msg-Id` header, and immediately returns a `202 Accepted` response with a WebSocket/SSE status URL.

### 2. Validation Worker Pool (Go Worker)

* **Consumer:** Durable Consumer on `invoices.v1.received`
* **Logic:** Validates seller and buyer ICE numbers via regex and checksum, checks TVA math, and validates items.
* **On Success:** Publishes to `invoices.v1.validated`.
* **On Failure:** Publishes to `invoices.v1.failed` with validation error details.

### 3. XML & PKI Signing Worker Pool (Go Worker)

* **Consumer:** Durable Consumer on `invoices.v1.validated`
* **Logic:** Formats data into OASIS UBL 2.1 XML schema and executes CGo/Native Go cryptographic functions to embed the XAdES signature using the company's PKI certificate key.
* **On Success:** Publishes to `invoices.v1.signed`.

### 4. DGI Network Adapter Worker Pool (Go Worker)

* **Consumer:** Durable Consumer on `invoices.v1.signed`
* **Logic:** Executes HTTPS client calls using Go’s native `net/http` pool (configured with mTLS certificates) to push signed XML to the government gateway.
* **Retry Strategy:** If DGI returns HTTP `503 Service Unavailable` or times out, the worker **NAK**s (Negative Acknowledgment) the NATS message with a delayed redelivery schedule. NATS handles retry backoff automatically.
* **On DGI Approval:** Publishes to `invoices.v1.cleared`.

### 5. Finalizer & Fan-out Workers (Concurrent Go Consumers)

When `invoices.v1.cleared` triggers, multiple independent Go consumers process the cleared state concurrently:

* **PDF Engine Worker:** Renders the visual PDF/A-3 document with the DGI-approved QR code stamp and stores it in S3/MinIO.
* **Accounting Sync Worker:** Updates the cabinet's real-time TVA ledger view in PostgreSQL.
* **Notification Worker:** Pushes real-time SSE/WebSocket updates to the client web UI and fires off customer emails or WhatsApp webhooks.

---

## 4. Real-Time Status Lookup via NATS KV Store

For instantaneous state tracking (without hammering the primary PostgreSQL database), Khetm utilizes **NATS Key-Value (KV) Store**.

* **Bucket Name:** `invoice-states`
* **Key Format:** `{seller_ice}:{invoice_number}`
* **State Values:** `RECEIVED` $\rightarrow$ `VALIDATED` $\rightarrow$ `SIGNED` $\rightarrow$ `CLEARED` (or `FAILED`)

```go
// Example Go worker snippet updating real-time state in NATS KV
func updateInvoiceState(kv nats.KeyValue, invoiceKey string, status string, metadata []byte) error {
    _, err := kv.Put(invoiceKey, []byte(fmt.Sprintf(`{"status":"%s","updated_at":%d}`, status, time.Now().Unix())))
    return err
}

```

The Go API service can query this NATS KV bucket in sub-millisecond time to serve polling or WebSocket status checks to the frontend.

---

## 5. Failure Recovery & Dead Letter Queue (DLQ)

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ NATS JETSTREAM FAULT-TOLERANCE PATTERN                                      │
├─────────────────────────────────────────────────────────────────────────────┤
│ 1. DGI Gateway Down / Outage:                                              │
│    Worker returns nats.NakWithDelay(30 * time.Second).                      │
│    Message stays in JetStream without data loss.                            │
│                                                                             │
│ 2. Max Retries Reached (e.g., Invalid ICE rejected by DGI):                │
│    Message moved to Dead Letter Subject: `invoices.v1.dlq`.                 │
│    Alert pushed to Accounting Firm Portal for manual intervention.           │
│                                                                             │
│ 3. Worker Pod Crash:                                                        │
│    JetStream AckWait timer expires; message re-assigned to another Go pod.  │
└─────────────────────────────────────────────────────────────────────────────┘

```

---

## 6. System Performance Targets with Go + JetStream

* **API Response Time (Ingest):** $< 15\text{ms}$ (Returns `202 Accepted` to client).
* **End-to-End Async Clearance (Normal Conditions):** $< 350\text{ms}$ from ingestion to DGI clearance receipt.
* **Concurrency:** A single Go + NATS node handles up to $10,000+$ concurrent invoice processing jobs with minimal RAM overhead ($\approx 100\text{MB}$ memory footprint for NATS + Go runtime).
* **DGI Rate Limiting:** Rate limiters configured directly on the NATS DGI Consumer prevent bursting requests to government endpoints beyond approved thresholds.