# Enrichment Worker

Executes deterministic clustering for parsed bank rows. This pipeline maps duplicate transactions and assigns historical vendor groupings across user invoices in the core database layer.

## Execution Trigger 

This worker boots inside the `sync` microservice process (`go/cmd/sync/main.go`). It subscribes to the JetStream subject `proof.accounting.cleanup.inserted`. This event guarantees the upstream `CSVMappingAgent` has flushed its parsed CSV data into the database.

## Architecture & Workflow 

1. **Deduplication:** Calls `cleanup.Deduplicator` to match row signatures, recording `duplicate_of` UUID correlations across rows.
2. **Database Write:** Updates `fignode.staging_transactions` assigning predicted vendor IDs and hashes.
3. **Redux Patch Generation:** 
   - Invokes `redux.Store.Reduce` using the actor identifier `"accounting.cleanup"`. It generates an `RFC 6902` state patch: `{"op": "add", "path": "/status", "value": "ENRICHED"}`.
   - Publishes this JSON sequence to JetStream `workflow.trace.{uuid}`. The frontend proxy streams this sequence to the user's React store to show progress updates.
4. **Handoff:** Issues `proof.accounting.cleanup.enrichment` to NATS to wake up downstream reconcilers (Expense and Revenue agents).
