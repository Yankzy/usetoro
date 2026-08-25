# PRD: Production Extraction of the Reconciliation Engine

> **FOR THE IMPLEMENTING AGENT**: You are tasked with migrating mathematically proven code from an evaluation harness into a production directory. **Do not attempt to rewrite, optimize, or interpret the core reconciliation or routing logic.** Your job is to strictly copy the canonical files 100% as-is and only refactor the import paths to ensure they work in their new standalone directory.

## 1. Context & Executive Summary

We have built a highly successful evaluation harness in `python-worker/app/reconciliation_eval/` that tests our AI reconciliation logic against complex accounting scenarios. This harness achieved a flawless 100% Exact Match performance by implementing a **Lexicographic Optimizer** (using Google OR-Tools CP-SAT) for both Routing and Reconciliation.

Our core finding (detailed in `white_paper.md`) establishes the **True Boundary**: Language models are excellent semantic engines but terrible mathematicians. To deploy this safely in production, we must extract the mathematically proven **Lexicographic Optimizer** and the **Lexicographic Routing Engine** into a hardened `reconciliation_prod/` directory.

The objective of this PRD is to guide you in creating a self-contained, Docker-ready module (`python-worker/app/reconciliation_prod/`) that can be executed in production. You must extract the core logic without exposing the production deployment to the evaluation scenarios, mock data, or test runners.

## 2. Core Principles & Constraints

1. **Zero Downstream Dependencies**: The `prod/` directory must be 100% self-contained. It cannot import any files from the parent `reconciliation_eval/` folder, ensuring it is fully insulated from the `.dockerignore` rules that exclude the evaluation harness.
2. **Do Not Decapitate Evals**: The existing evaluation harness must continue to function perfectly. We will **copy** the required files into `prod/`, rather than moving them, preserving the A/B testing harness for future research.
3. **Preserve Mathematical Immunity**: The production extraction must rigorously preserve the multi-tiered Lexicographic Objectives that made the system immune to "Utility Farming" and "Penny Pincher" capacity traps.

## 3. Reference Architecture

The architecture relies on the foundational concepts proven in our evaluations:
- **Reference**: `python-worker/app/reconciliation_eval/white_paper.md`
- **Reference**: `python-worker/app/reconciliation_eval/eval.md`

The production system will execute the exact same 4-phase workflow proven in the evaluations:
1. **Deterministic Feasibility**: `O(N * T)` subset-sum generation to define valid mathematical bounds.
2. **LLM Semantic Scoring**: The LLM assigns confidence scores (0-1000) to mathematically viable paths.
3. **Lexicographic Routing (CP-SAT)**: Assigns book items to isolated bank accounts, strictly maximizing monetary value over semantic utility to prevent capacity exhaustion.
4. **Lexicographic Reconciliation (CP-SAT)**: Solves the final matching state within each account, enforcing Occam's Razor (consolidation penalty) to defeat fragmented hallucinations.

## 4. Target Directory Structure

We will create a new directory at `python-worker/app/reconciliation_prod/`.

```text
reconciliation_prod/
├── __init__.py
├── api.py                  # Main Production Entrypoint
├── domain/
│   ├── __init__.py
│   ├── bank.py             # BankItem models (copied from domain/)
│   ├── book.py             # BookItem models (copied from domain/)
│   ├── hypothesis.py       # ReconciliationHypothesis (copied from domain/)
│   └── state.py            # ProposedState (copied from domain/)
├── routing/
│   ├── __init__.py
│   ├── feasibility.py      # DP Subset-Sum Feasibility (copied from routing/)
│   ├── scorer.py           # LLM Semantic Scoring (copied from routing/)
│   ├── optimizer.py        # Lexicographic CP-SAT Router (copied from routing/)
│   └── engine.py           # Multi-account orchestration (copied from routing/orchestrator.py)
└── reconciliation/
    ├── __init__.py
    ├── candidate_generation.py  # Exact CP-SAT candidates (copied from agent/)
    ├── cp_sat.py                # Lexicographic Solver (copied from optimizer/)
    ├── model.py                 # Lexicographic Objective (copied from optimizer/)
    ├── semantic_engine.py       # Extracted scoring logic (from reconciliation_agent.py)
    └── validation.py            # Deterministic Invariants (copied from validation/)
```

## 5. Orchestrator Integration (Go-style Activity Workers via NATS)

To integrate this mathematically perfect reconciliation engine into the core product, the Python codebase will expose **two standalone Go-style Activity Workers** (as seen in `pcm_worker.go`). 

The Routing logic and the Reconciliation logic must be fully decoupled. This allows the Orchestrator to trigger Routing independently—either before or after the ASE package runs—without automatically forcing reconciliation to run.

### Activity 1: The Routing Worker
1. **Activity Type**: `workers.routing`
2. **Derived NATS Subject**: `worker.inbox.routing`
3. **Execution**: The Python router receives a `core.Envelope` (REQUEST), extracts the `BankItem` and `BookItem` objects, and executes Phase 1 (Feasibility), Phase 2 (Scoring), and Phase 3 (Lexicographic Routing).
4. **Response**: It publishes the `GlobalRoutingState` back to the orchestrator via `msg.Reply` and acknowledges the message.

### Activity 2: The Reconciliation Worker
1. **Activity Type**: `workers.reconciliation`
2. **Derived NATS Subject**: `worker.inbox.reconciliation`
3. **Execution**: The Python reconciler receives a `core.Envelope` (REQUEST) containing already-routed buckets (a specific Bank Account and its assigned Book Items). It executes Phase 4 (Lexicographic Reconciliation).
4. **Response**: It publishes the `ProposedState` back to the orchestrator via `msg.Reply` and acknowledges the message.

## 6. Implementation & Refactoring Steps

> **CRITICAL INSTRUCTION FOR IMPLEMENTING AGENT**: Do not rewrite the logic inside the files below. The math is already perfect. You must copy the core code exactly as it is, and only change the `import` statements to point to the new `reconciliation_prod.*` namespace. 
> 
> **DO NOT COPY**: The `scenarios/` directory, the mock data, or the `run.py` evaluator.

### Step 1: Copy and Isolate Domain Models
1. **Copy 100% as-is**: `bank.py`, `book.py`, `hypothesis.py`, and `state.py` from `domain/` into `reconciliation_prod/domain/`.
2. **Refactor Imports**: Update all internal imports within these files to use absolute `reconciliation_prod.domain.*` paths. This severs ties to the parent folder.

### Step 2: Extract the Lexicographic Routing Engine
1. **Copy 100% as-is**: `feasibility.py`, `scorer.py`, and `optimizer.py` from `routing/` into `reconciliation_prod/routing/`.
2. **Copy as-is**: `routing/orchestrator.py` into `reconciliation_prod/routing/engine.py`.
3. **Refactoring**: 
   - Strip out any evaluation-specific `logger.info` statements that mention test metrics or ground-truth comparisons.
   - **Crucial**: Ensure `optimizer.py` strictly retains the Lexicographic Objective (`W_MONEY = 1_000_000`). Do not modify the math.
   - Rewrite all imports to reference `reconciliation_prod.domain.*` and `reconciliation_prod.routing.*`.

### Step 3: Extract the Lexicographic Reconciliation Engine
1. **Copy 100% as-is**: `candidate_generation.py`, `optimizer/model.py`, `optimizer/cp_sat.py`, and `validation/deterministic.py` into `reconciliation_prod/reconciliation/`.
2. **Semantic Engine Refactor**: Do NOT copy `reconciliation_agent.py` verbatim. Production does not need a conversational loop or an OpenAI "tool" schema. Instead, extract *only* the prompt assembly and LLM API call logic into a streamlined `reconciliation_prod/reconciliation/semantic_engine.py` whose sole responsibility is returning scored hypotheses.
3. **Crucial**: Ensure `model.py` strictly retains the Lexicographic tiers and Consolidation Penalty (`HYPOTHESIS_PENALTY = -10_000`). Do not modify the math.
4. **Refactor Imports**: Update all imports to `reconciliation_prod.*`.

### Step 4: Build the Independent NATS Workers (`routing_worker.py` and `reconciliation_worker.py`)
Create two distinct NATS worker interfaces:
1. `reconciliation_prod/routing_worker.py`: Subscribes to `worker.inbox.routing`. Responsible *only* for multi-account allocation.
2. `reconciliation_prod/reconciliation_worker.py`: Subscribes to `worker.inbox.reconciliation`. Responsible *only* for solving the final matches within an isolated account.
