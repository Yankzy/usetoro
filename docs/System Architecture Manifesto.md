# System Architecture Manifesto: Core Axioms for Fault-Tolerant Deterministic Engineering

## Introduction

This document outlines the foundational engineering philosophy and architectural axioms that govern our platform. Because our system orchestrates non-deterministic, probabilistic artificial intelligence (LLM agents) within high-stakes financial, accounting, and workforce workflows, we cannot afford structural drift, data corruption, or unhandled states.

The following axioms are designed to enforce absolute predictability, mathematical integrity, and self-healing resilience across our Go-based Directed Acyclic Graph (DAG) runtime. Every developer is expected to design, review, and commit code in strict alignment with these principles.

---

## Axiom 1: Absolute Parameterization and Boundary Enforcement

A system cannot maintain order unless its inputs, resource caps, and internal types are explicitly measured and bounded *before* runtime execution. Order is the continuous containment of entropy.

### 1. Numerical Determinism

* **The Rule:** Floating-point arithmetic (`float64`) is strictly prohibited for currency, ledger states, and unit balances.
* **The Execution:** Every financial value must be explicitly parameterized as a fixed-point integer (`int64`) representing the absolute smallest sub-unit (e.g., cents), or encapsulated within an immutable, arbitrary-precision decimal struct.

### 2. Strict Input/Output Parameterization

* **The Rule:** Loose data structures, generic interfaces (`interface{}` / `any`), and dynamic untyped maps are prohibited along our DAG execution edges.
* **The Execution:** Every edge connecting two operational nodes must be a strongly typed Go struct enforced via strict schema serialization. If an AI agent attempts to emit a payload that deviates by a single field from the predetermined schema contract, the orchestrator must intercept and reject the state mutation before it propagates downstream.

### 3. Execution Sandboxing

* **The Rule:** Non-deterministic compute blocks (LLM API calls, agent reasoning loops) must never be given open-ended execution parameters.
* **The Execution:** Every asynchronous task must be wrapped in a strictly bounded Go `context.Context`. Developers must enforce hard-coded deadlines, execution timeouts, and strict runtime token/cost quotas. If an agent fails to yield a valid, schema-compliant consensus before these limits are breached, the execution framework must force-terminate the operation.

---

## Axiom 2: Architectural and Agentic Duality (The Dyad Principle)

No single component, agent, or state should ever operate in isolation. Systemic reliability is achieved by splitting critical functions into balanced, interdependent pairs that validate and constrain one another.

```
[System Input] ──> [ NODE EXECUTION LAYER ]
                          │
                          ├──> Proposer Engine (Generates Payload)
                          │         │
                          │         ▼ (Payload Stream)
                          └──> Verifier Engine (Adversarial Audit)
                                    │
                                    └──> [Reconciliation Engine] ──> [System Output]

```

### 1. Agentic Duality (The Proposer-Verifier Model)

* **The Rule:** No functional node in our workflow graph may rely on a single autonomous AI agent to execute and commit a task.
* **The Execution:** Every node execution block must be decoupled into an inseparable twin-engine setup:
* **The Proposer:** An agent optimized for generative completion, processing speed, and execution.
* **The Verifier:** An independent, adversarial agent configured with contrasting, restrictive parameters. Its sole function is to audit the Proposer's output for logical discrepancies, hallucinations, and boundary violations. A node cannot emit a valid output until both engines achieve mathematical consensus.



### 2. Topological Duality (Execution vs. Guardrail)

* **The Rule:** The primary execution pipeline must not manage its own security, budgeting, or permission validation states.
* **The Execution:** Our engine maintains a dual-plane graph architecture. The *Execution Plane* routes task sequences and data payloads. Asynchronously, a parallel *Guardrail Plane* tracks authorizations, budgets, and compliance risk parameters. These planes communicate via non-blocking channels; the execution plane cannot advance an edge without real-time permission sign-off from its topological guardrail dual.

### 3. State Duality (The Mirror Log)

* **The Rule:** System state can never be represented as a single, mutable record.
* **The Execution:** All transaction logs and distributed pipeline states must be stored as an immutable, strictly paired data block containing two distinct sub-objects:
* **The Intent:** The exact declarative instruction the system *attempted* to execute.
* **The Fact:** The actual, verified response returned by the database transaction or external API.



---

## Axiom 3: Dynamic Equilibrium and Transactional Recovery

A resilient system must possess internal, self-correcting mechanisms that automatically restore structural and financial balance whenever external dependencies or third-party integrations fail.

### 1. The Distributed Saga Pattern

* **The Rule:** For every forward-facing operational node that modifies external state (e.g., initiating a banking wire, mutating an HR database), an identical, backward-facing compensating action must exist.
* **The Execution:** When a downstream DAG node fails or times out, the platform must not crash or leave data in a fractured state. The orchestrator must automatically execute backward along the graph, triggering compensating handlers to cleanly undo partial writes, execute transaction rollbacks, and return the entire multi-tiered system to a stable equilibrium.

### 2. Continuous Reconciliation Loops

* **The Rule:** The platform must constantly audit its own reality against external data providers.
* **The Execution:** Background goroutines must run automated, continuous reconciliation loops that compare the `Intent` logs against the `Fact` logs. If a discrepancy is detected (e.g., a bank API timed out, but the internal intent state was marked as processing), the reconciliation engine must automatically freeze the affected workflow edge and safely synchronize the states.

---

## Axiom 4: Total Structural Integrity and Continuous Verification

True architectural excellence is defined by the absolute absence of logical leaks, component friction, or hidden failures under intense operational load.

### 1. Zero-Tolerance for Silent Errors

* **The Rule:** Swallowing errors, using blank identifiers (`_`), or ignoring return values is a critical architectural violation.
* **The Execution:** Go’s explicit error handling pattern (`if err != nil`) must be treated as a strict governance protocol. Errors must either be completely handled, wrapped with structural context, or passed up to the global transactional rollback loop. If a state transition cannot be explicitly verified, the system must default to a safe, non-destructive halt.

### 2. Invariant Auditing

* **The Rule:** System invariants must be checked continuously, not just during localized unit tests.
* **The Execution:** Our workflow engines must run end-to-end trace auditing on every active DAG execution. Code should be written under the assumption that it is being watched by a continuous stress-tester. If any logical dissonance or structural inconsistency is detected between internal components, the system must prioritize data integrity over processing uptime, immediately alerting engineering teams while preserving a pristine audit trail.