# Audit of the Current CSV Pipeline

The current CSV Pipeline relies on decentralized choreography over NATS:
1. **API Trigger**: `HandleFileIngestion` uploads a CSV, saves a `CleanupSession` to the DB, and emits a `CFP` (Call for Proposal) envelope on `events.accounting.1.cleanup`.
2. **CSV Mapping Agent (Agent)**: Subscribes, negotiates via `PROPOSE`, uses LLM to map columns, executes via the Redux `BaseAgent` wrapper, and emits a `proof.accounting.cleanup.columns` (`INFORM`) event.
3. **CSV Mapping Worker (Worker)**: Listens for the proof, writes the mapped rows to PostgreSQL (`InsertCleanupRow`), and publishes `proof.accounting.cleanup.inserted`.
4. **Reconcile Revenue/Expense Agents (Agents)**: Trigger downstream based on `ENRICHED` rows, executing logic via bounded contexts, saving their own output, and emitting `proof.accounting.cleanup.reconcile.[revenue/expense]`.
