# Python API Reference

This document provides a technical reference for the core Python classes, services, commands, and utility functions in Toro's bookkeeping runtime.

---

## 1. Application Services & Entry Points

### `run_bookkeeping_session`
- **Import Path**: `bookkeeping_state.session.service`
- **Visibility**: Public (Top-level application entry point)
- **Signature**:
  ```python
  def run_bookkeeping_session(
      company_id: str,
      session_id: str | None = None,
      issued_at: datetime | None = None,
  ) -> SessionResult
  ```
- **Purpose**: Instantiates the production application service, executes preflight readiness probes against NATS and the Go ASE worker, runs the full bookkeeping session, and returns the sealed `SessionResult`.
- **Example**:
  ```python
  from bookkeeping_state.session.service import run_bookkeeping_session

  result = run_bookkeeping_session(
      company_id="toro-synthetic-bookkeeping",
      session_id="session-prod-2026-04",
  )
  if result.is_success:
      print(f"Session succeeded at revision P{result.final_persistence_revision}")
  ```

### `BookkeepingApplicationService`
- **Import Path**: `bookkeeping_state.session.service`
- **Visibility**: Public
- **Methods**:
  - `run_session(company_id: str, session_id: str | None = None) -> SessionResult`
- **Constructor Dependencies**:
  - `repository: BookkeepingRepository`
  - `residual_bank_categorizer: ResidualBankCategorizer | None`
  - `routing_service: RoutingService`
  - `dag_classifier: AseClassifier`
  - `reconciliation_service: ReconciliationService`

### Production Factory Helpers
- **`create_production_bookkeeping_application_service(nats_url=None) -> BookkeepingApplicationService`**
  (`bookkeeping_state.session.service`)
  Instantiates the service with fully configured production dependencies, including `NatsAseBankCategorizer` connected to the active NATS bus.
- **`create_production_session(...) -> BookkeepingSession`**
  (`bookkeeping_state.session.factory`)
  Constructs a single `BookkeepingSession` instance with production wiring.

---

## 2. Session Coordinator & Results

### `BookkeepingSession`
- **Import Path**: `bookkeeping_state.session.bookkeeping_session`
- **Visibility**: Public
- **Signature**:
  ```python
  class BookkeepingSession:
      def __init__(
          self,
          *,
          state: BookkeepingState,
          engine: TransitionEngine,
          routing_service: RoutingService,
          dag_classifier: AseClassifier,
          reconciliation_service: ReconciliationService,
          payment_application_service: PaymentApplicationService | None = None,
          residual_bank_categorizer: ResidualBankCategorizer | None = None,
          hydrator: BookkeepingHydrator | None = None,
      ) -> None
      def run(self) -> SessionResult
  ```
- **Side Effects**:
  - Mutates and closes the provided `BookkeepingState` (`state.is_closed = True`).
  - Commits transactions to PostgreSQL via `TransitionEngine`.
  - Sends NATS requests to the Go ASE worker.

### `SessionResult`
- **Import Path**: `bookkeeping_state.session.result`
- **Attributes**:
  - `company_id: str`
  - `session_id: str`
  - `is_success: bool`
  - `starting_persistence_revision: int`
  - `final_persistence_revision: int`
  - `failure_stage: FailureStage | None`
  - `failure_reason: str | None`
  - `routing_result: SessionStageResult`
  - `payment_application_result: SessionStageResult`
  - `reconciliation_result: SessionStageResult`
  - `residual_result: SessionStageResult`
  - `executed_payment_application_ids: tuple[str, ...]`
  - `executed_residual_posting_ids: tuple[str, ...]`
  - `unresolved_residual_hold_count: int`

---

## 3. In-Memory State & Queries

### `BookkeepingState`
- **Import Path**: `bookkeeping_state.state.bookkeeping_state`
- **Visibility**: Public
- **Key Attributes**:
  - `revision: int` (Local state revision $S_k$)
  - `persistence_revision: int` (Database persistence revision $P_k$)
  - `is_closed: bool` (Set to `True` when closed)
  - `bank_items: dict[str, BankItem]`
  - `book_items: dict[str, BookItem]`
  - `reconciliations: dict[str, Reconciliation]`
  - `residual_bank_classifications: dict[str, ResidualBankClassificationDecision]`
  - `residual_bank_postings: dict[str, ResidualBankPosting]`
- **Key Methods**:
  - `close() -> None`: Seals the state, preventing further reads or transitions.
  - `get_residual_bank_posting_for_decision(decision_id: str) -> ResidualBankPosting | None`
  - `get_residual_bank_posting_for_reconciliation(reconciliation_id: str) -> ResidualBankPosting | None`

### `BookkeepingQueries`
- **Import Path**: `bookkeeping_state.state.queries`
- **Visibility**: Public (Primary read projection layer)
- **Key Methods**:
  - `bank_remaining_units(bank_item_id: str) -> int`: Returns positive remaining unreconciled units.
  - `book_remaining_units(book_item_id: str) -> int`: Returns remaining unallocated book units.
  - `residual_unmatched_bank_items() -> list[tuple[BankItem, int]]`: Returns all bank items with positive remaining units.
  - `active_residual_bank_classification(bank_item_id: str) -> ResidualBankClassificationDecision | None`: Returns the active decision tip.
  - `is_residual_bank_classification_posted(decision_id: str) -> bool`: Checks if durable posting provenance exists.

---

## 4. Hydration & Persistence Repository

### `BookkeepingHydrator`
- **Import Path**: `bookkeeping_state.hydration.hydrator`
- **Signature**:
  ```python
  class BookkeepingHydrator:
      def __init__(self, repository: BookkeepingRepository) -> None
      def hydrate(self, *, company_id: str, session_id: str | None = None) -> BookkeepingState
  ```
- **Purpose**: Reads a point-in-time snapshot from the repository and constructs an open `BookkeepingState` at revision $S_0$.

### `BookkeepingRepository`
- **Import Path**: `bookkeeping_state.persistence.repository`
- **Methods**:
  - `load_snapshot(company_id: str) -> BookkeepingSnapshot`
  - `commit(company_id: str, expected_revision: int, write_set: PersistenceWriteSet) -> PersistenceCommitResult`

---

## 5. Transition Engine & Commands

### `TransitionEngine`
- **Import Path**: `bookkeeping_state.transitions.engine`
- **Key Methods**:
  - `apply(state: BookkeepingState, command: BookkeepingCommand) -> TransitionResult`
  - `apply_batch(state: BookkeepingState, batch: TransitionBatch) -> BatchTransitionResult`

### Core Commands

| Command Class | Import Path | Target Subsystem | Key Fields |
|---|---|---|---|
| `ApplyPaymentCommand` | `bookkeeping_state.domain.commands` | Stage 1 Payment Application | `payment_application_id`, `plan_intent`, `capability` |
| `RecordResidualBankClassificationCommand` | `bookkeeping_state.domain.commands` | Residual Categorization | `decision_id`, `bank_item_id`, `status`, `account_code`, `confidence`, `hold_reason` |
| `PostResidualBankClassificationCommand` | `bookkeeping_state.domain.commands` | Residual Posting Loop | `decision_id`, `bank_item_id`, `bank_account_id`, `account_code`, `residual_amount_units` |
| `InvalidateReconciliationCommand` | `bookkeeping_state.domain.commands` | Stage 2 Invalidation | `reconciliation_id`, `reason` |
| `InvalidateResidualBankClassificationCommand` | `bookkeeping_state.domain.commands` | Residual Decision Invalidation | `decision_id`, `invalidation_id`, `reason` |

---

## 6. Monetary Scaling & Formatting Helpers

All internal currency calculations use exact integer solver units with a scale factor of **10,000** (e.g. $78,500,000 = 7,850.00$ MAD).

### Module: `bookkeeping_state.domain.money`

```python
def format_money(
    value: int | AmountUnits | ResidualAmountUnits | Decimal | None,
    currency: str = "MAD",
) -> str:
    """
    Renders integer solver units or Decimal as standard localized currency.
    Examples:
        78_500_000  -> "7,850.00 MAD"
        1_500_000   -> "150.00 MAD"
        34_000_000  -> "3,400.00 MAD"
        100_000_000 -> "10,000.00 MAD"
    """

def solver_units_to_decimal(value: int | AmountUnits | ResidualAmountUnits) -> Decimal:
    """Converts integer units to exact Decimal major units (divides by 10,000)."""

def major_units_to_solver_units(value: str | Decimal) -> int:
    """Converts human-readable decimal amount to integer units (multiplies by 10,000)."""
```

---

## 7. NATS Transport Client

### `NatsAseBankCategorizer`
- **Import Path**: `bookkeeping_state.bank_categorization.nats_bank_categorizer`
- **Protocol**: Implements `ResidualBankCategorizer`
- **Key Methods**:
  - `check_readiness() -> None`: Sends an empty ping to verify NATS connectivity and worker subscription before session starts.
  - `categorize_view(view: ResidualBankCategorizationView) -> BankCategorizeResponse`: Dispatches the bounded residual evaluation wave over NATS.
