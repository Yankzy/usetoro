Below is a rewritten PRD in full narrative form, with structure but written primarily in clear, continuous English.

---

# PRODUCT REQUIREMENTS DOCUMENT (PRD)

## AI-Powered Accounts Payable and Accounts Receivable Agent System with Invoice-Centric Payment Orchestration

---

## 1. Product Vision and Overview

The purpose of this product is to redefine how small and medium businesses manage financial obligations by eliminating the traditional separation between invoicing, payment execution, and accounting reconciliation. Today, these processes are fragmented across different systems: invoices are created in accounting software, payments are executed through banking interfaces or payment processors, and reconciliation is performed manually or semi-automatically after the fact. This fragmentation creates ambiguity, delays, and operational inefficiency.

This product introduces a unified system where invoices are not merely documents but become the central source of truth for financial activity. Every payment originates from an invoice, every invoice is tracked through its full lifecycle, and both the sender and receiver share a synchronized view of financial reality at all times. The system is designed as a dual-agent architecture, consisting of an Accounts Payable (AP) agent and an Accounts Receivable (AR) agent, both operating on top of a shared invoice-payment ledger.

At a deeper level, the system is not just accounting software. It is a financial coordination layer that ensures that economic intent, execution, and recording are aligned in a deterministic way. The long-term goal is to eliminate the need for forensic reconciliation entirely.

---

## 2. Problem Statement

Modern accounting systems fail not because they lack features, but because they attempt to reconstruct financial truth after transactions have already occurred in fragmented systems. A single economic event, such as an invoice being paid, is interpreted differently by multiple systems: the bank sees a generic transfer, the accounting system sees an imported transaction, and the business sees an invoice that must be manually matched to a payment.

This mismatch creates several structural problems. First, reconciliation becomes a manual or semi-automated process that is prone to errors and delays. Second, there is no shared real-time truth between payer and receiver, which means disputes and ambiguity persist. Third, automation systems built on top of this fragmented data cannot reliably make decisions, because they operate on incomplete or inconsistent information.

The core problem is therefore not computation, but coordination. Financial systems lack a shared execution layer that binds invoices, payments, and accounting entries into a single coherent lifecycle.

---

## 3. Product Philosophy

The product is built on a single principle: financial events should be created as structured, shared objects before money moves, not reconstructed afterward.

This means that an invoice is not a passive PDF or record, but an active financial object that governs the lifecycle of payment. A payment is not an isolated bank transaction, but a state transition within an invoice lifecycle. Accounting is not a post-processing step, but an emergent property of the system.

In practical terms, this philosophy enforces that no payment can exist without an invoice, and no invoice can remain abstract once a payment intent has been created. Every financial event becomes traceable, structured, and mutually visible between parties.

---

## 4. System Architecture Overview

The system is composed of three conceptual layers.

The first layer is the application layer, which contains the AP and AR agents. These agents are responsible for user interaction, invoice creation, payment initiation, scheduling, reminders, and communication between counterparties. They act as intelligent interfaces that translate human intent, including voice input, into structured financial objects.

The second layer is the financial orchestration layer. This layer manages payment intents, authorization flows, execution scheduling, and settlement tracking. It connects invoices to actual money movement through external payment infrastructure, while maintaining an internal representation of every financial event.

The third layer is the ledger layer, which serves as the system of record. This ledger is not derived from bank data imports, but generated in real time as a byproduct of invoice and payment lifecycle events. It ensures that both AR and AP sides always see a consistent accounting state.

---

## 5. Core User Experience

The user experience begins with invoice creation. A user, typically a business owner or CPA managing clients, creates an invoice either through a structured form or through natural voice input. For example, a user might say: “Create an invoice for 3,000 dollars for consulting services for ABC Corp due in 30 days.” The system converts this into a structured invoice object.

Once created, the invoice is immediately sent to the counterparty. The receiver is not simply given a document, but a structured financial request that includes payment options, due dates, and the ability to accept, dispute, or schedule payment. This creates a shared understanding of the obligation.

On the accounts payable side, the receiver sees an invoice inbox managed by the AP agent. Each invoice is an actionable item that can be approved for payment, scheduled for later execution, or set for automatic payment under predefined rules. The AP agent ensures that payments are executed according to user intent while maintaining synchronization with the invoice lifecycle.

When payment is initiated, the system creates a payment intent that is explicitly linked to the invoice. This payment intent is then executed through an integrated payment rail provider, which in the MVP is a single ACH-capable infrastructure provider. The execution of the payment updates the system in real time through event-driven webhooks, ensuring that both AR and AP views are updated simultaneously.

---

## 6. AP Agent Responsibilities

The Accounts Payable agent is responsible for all outgoing financial obligations. It acts as a decision-making layer for the payer, transforming incoming invoices into structured payment actions. It maintains an organized inbox of all received invoices, provides recommendations for payment scheduling based on due dates and cash flow, and allows users to approve or automate payments.

The AP agent also manages authorization states, ensuring that payments that require approval are explicitly confirmed by the user before execution. Once a payment is executed, it continuously tracks settlement status and updates both the user interface and the underlying ledger system.

Importantly, the AP agent is not just a user interface layer. It is a behavioral layer that learns how a business prefers to pay vendors and enforces those preferences consistently across all incoming obligations.

---

## 7. AR Agent Responsibilities

The Accounts Receivable agent manages outgoing invoices and ensures that financial obligations are tracked from the issuer’s perspective. It allows users to generate invoices through structured input or voice, send them to counterparties, and monitor their lifecycle in real time.

The AR agent is responsible for tracking whether invoices are viewed, accepted, scheduled for payment, or completed. It also handles automated reminders and follow-ups, reducing the need for manual chasing of payments.

From an accounting perspective, the AR agent ensures that all issued invoices are immediately reflected in the system ledger, even before payment occurs, providing real-time visibility into expected revenue.

---

## 8. Payment Execution and Settlement Flow

Payment execution begins with a payment intent, which is always linked to an invoice. This payment intent defines the amount, method, schedule, and authorization type. The system ensures that no payment is executed without a valid invoice reference.

Once a payment is initiated, it is sent to the external payment rail provider. The MVP assumes a single ACH-based provider to reduce integration complexity and ensure deterministic behavior. The provider executes the transfer, and settlement events are returned via webhooks.

These events are then consumed by the system to update the payment status and trigger ledger updates. At this point, both AP and AR agents see the same final state, ensuring consistency across the system.

---

## 9. Shared Ledger and Accounting Model

The ledger is the most critical component of the system. It is designed as an event-sourced system that records every financial state transition in relation to invoices and payments. Each ledger entry is immutable and linked to either an invoice or a payment event.

The key design principle is that accounting is not derived from external bank statements but from internal system events. This ensures that financial reporting is consistent, real-time, and free from reconciliation ambiguity.

Both AP and AR perspectives are projections of the same underlying ledger, ensuring that there is never a discrepancy between what one party believes and what the other party sees.

---

## 10. AI and Automation Layer

The system incorporates AI primarily in two areas. First, it enables natural language or voice-driven invoice creation. Users can describe financial transactions verbally, and the system converts them into structured invoices with correct fields such as amount, description, and due date.

Second, the AI layer assists in payment behavior optimization. For example, the AP agent may suggest optimal payment timing based on due dates or cash flow constraints, while the AR agent may suggest reminders or escalation strategies for overdue invoices.

However, AI is not the core of the system. It is an interface enhancement layer built on top of a structured financial coordination engine.

---

## 11. Non-Functional Requirements

The system must be fully auditable, meaning every financial state transition must be traceable and reproducible. All ledger entries must be immutable. Payment operations must be idempotent to prevent duplication or inconsistency in financial execution. Webhook handling must be reliable, with retry mechanisms in case of failure.

Security is critical, particularly in handling bank authorization and payment initiation. All sensitive data must be encrypted in transit and at rest. The system must also be designed to handle eventual regulatory requirements related to payment initiation and financial data handling.

---

## 12. MVP Scope and Constraints

The MVP deliberately avoids complexity in several areas. It does not include full bank transaction aggregation, nor does it require deep integration with multiple payment rails. It does not include wallet-based stored balances or multi-currency settlement optimization. It also avoids tax automation and ERP-level financial reporting.

The goal is to remain focused on a single invariant: invoices and payments must be structurally linked and shared between counterparties in real time.

---

## 13. Success Criteria

Success is measured by the percentage of financial transactions that remain fully inside the system without requiring external reconciliation. Another key metric is the speed at which invoices can be created and paid. A strong signal of success is CPA adoption, as accountants benefit significantly from reduced reconciliation workload.

Ultimately, the success of the system is defined by whether financial ambiguity is eliminated at the point of transaction creation rather than resolved after the fact.

---

## 14. Strategic Outcome

If executed correctly, this system evolves from a simple invoicing and payment tool into a financial coordination layer for small and medium businesses. Over time, it becomes a network where economic relationships between businesses are structurally encoded, creating a dense financial graph that improves automation, trust, and efficiency.

The long-term implication is not just better accounting software, but a new form of financial infrastructure where accounting becomes an emergent property of structured economic interaction rather than a separate process.

---

If you want next, I can turn this into:

* a system architecture diagram (clean, no fluff)
* or a database schema (production-ready)
* or a first engineering sprint plan (what to build in week 1, 2, 3)
