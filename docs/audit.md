# Toro Ecosystem & Fignode Clients – Technical Audit

## Executive Summary: The Platform of Work for Digital Labor
According to MIT, 95% of enterprise GenAI pilots deliver zero P&L impact. Gartner projects 40% of all agentic AI projects will be canceled by 2027. The root cause is a failure of systems architecture: the industry is attempting to plug high-entropy, unpredictable LLMs directly into zero-entropy, deterministic enterprise ledgers. 

**Toro has solved this.** This document outlines the architecture of the Toro Protocol—an event-sourced, decentralized AI operating system built in Go. We have decoupled the reasoning engine (LLM) from the state machine (Ledger). By enforcing strict RFC 6902 state mutations and wrapping banking-grade financial intelligence in a gamified, high-velocity UX (Fignode), we have built the foundational infrastructure where enterprises will securely deploy, manage, and audit their AI workforce. The core physics are solved, and the tolling mechanics are live. 

---

## 1. Architecture Overview

### Backend Platform (Toro)
- **Core Microservices**: Go handles the "Gate" (stateless HTTP ingress -> NATS), "Protocol" (State Machine/Logic), "Sync" (Background workers/limiters), and "Realtime" (WebSockets) operations.
- **Workflow Engine & Orchestrator**: A declarative orchestration layer using YAML blueprints (`tap/workflows`) to define multi-step execution graphs. A central Go Orchestrator manages the `WorkflowInstance` state-machine lifecycle.
- **The Vault**: NATS JetStream persists events and acts as the immutable task queue. Uses a **Stateless Dispatcher Pattern** with dynamic trigger subject reconciliation.
- **Data & Intelligence**: PostgreSQL with the `pgx` driver is the relational source-of-truth. Python (Flask/Workers) operates the "Refinery" for OCR and heavy analytics.
- **Toro Agent Protocol (TAP)**: A rigorous implementation of the **FIPA Actor Model** (CFP, PROPOSE, ACCEPT, INFORM). Standardizes decentralized agent negotiation, cryptographic proof submission, and automated contract execution.

### Desktop Client (Fignode Pro)
- **Framework**: Wails (Go) and React 18 (TypeScript), enabling native desktop performance with shared web components (Tailwind CSS v4, Radix UI).
- **Core Strategy**: Acts as the command center for the "AI Staff" to oversee intelligence.
- **Local Resilience**: Built-in SQLite database (`pkg/storage`) to persist encrypted sessions, OAuth tokens, and act as a fast cache layer. Let's the UI sync directly with local APIs exposed by Go (Wails).

### Mobile Client (Fignode App)
- **Framework**: React Native with `@react-navigation` to power Wallet, Gamified Streams, and Management.
- **Core Strategy**: A "Tinder-style UX" for junior accountants/bookkeepers to swipe and classify high-velocity AI-proposed transactions.
- **State Mechanics**: Centralized Context (`ToroContext.js`) managing gamification mechanics (trust scores, accuracy, streaks) and high-performance `react-native-reanimated` swipe-decks.

---

## 2. Implemented Features (What is Currently Built)

### Core Platform Infrastructure
- **Ingestion & Messaging**: Webhook ingestion buffers push to NATS for isolated execution.
- **Universal Ingestion (WebhooksWorker)**: A deterministic worker enabling atomic HTTP side-effects and ingestion of arbitrary JSON/Form payloads from external platforms into the TAP internal bus.
- **Orchestration & Dispatcher Pattern**: We deployed a highly efficient, centralized Manager (`manager.go`) and Orchestrator. The Orchestrator dynamically provisions and monitors `Init()`, `Subscriptions()`, and `Handle()` lifecycles across all listeners. This drives idle worker memory usage down to $O(1)$ and provides instant, synchronized context cancellation across the node.
- **Zero-Data-Loss NATS Pipeline**: NATS JetStream consumers implemented across all agents and workers. Messages require explicit acknowledgements and use poison-pill logic (`msg.Term()`) to guarantee state machine durability.
- **Redux Engine**: An in-memory state compilation mechanism. It acts as a pure reducer, applying RFC 6902 JSON Patches sequentially over NATS. It enforces JSON Schema validation and optimistic concurrency (`test` operator) to prevent race conditions. Every Redux patch generated dynamically deducts a Micrion inference toll.
- **Micrion Tolling Architecture**: Configured a 1,616 Micrion toll gating infrastructure operations. A Micrion ($\mu C$) is a prepaid, tokenized unit of compute (1 $\mu C$ = $0.000001 USD). NATS writes, Redux state sequences, and PostgreSQL transactions are metered universally across the ecosystem.
- **Real-time Status & UX Hydration**: A dedicated NATS-based channel (`workflow.status.*`) broadcasts state-machine transitions (started, step_completed, finished) to the Gate for real-time WebSocket broadcasting to frontend clients.
- **API Capabilities**: GraphQL schema mapped for tenants, users, cleanup-sessions, transactions, and vendors. GraphQL resolvers structure `RawAmount` typings.
- **Fignode Service**: Go service (`cmd/fignode`) handling accounting classification, UX states (badges, streaks, leaderboards), and email SMTP. 
- **Determinism & The Rule Engine**: Implemented a performant keyword-based mapping system that compiles user-defined accounting rules into memory for instant, zero-LLM classification of Irrelevant transactions.
- **The Autonomous Daemon (`daemon.go`)**: This is the mission control keeping the AI workforce alive and stable. Instead of fragile scripts that silently fail, the Daemon enforces strict "fail-fast" survival rules—if any critical component crashes, it safely shuts down the entire node rather than leaving zombie processes corrupting the ledger. It also handles "hot reloads", meaning we can upgrade AI agents or change system settings on the fly without dropping a single active customer connection. Finally, a built-in 5-second grace period ensures that if the server is forced to restart, any active Stripe payments or OpenAI thoughts are cleanly saved to the database first, mathematically guaranteeing zero data loss.

### Autonomous AI Ecosystem (Agent SDK)
- **Domain-Agnostic Agent Substrate**: Introduced a vertical-agnostic SDK for LLM-driven reasoning. Agents (e.g., `IntentExtractorAgent`) are now fully decoupled from static Go primitives. They utilize task-scoped JSON schemas and YAML-scoped system prompts, allowing the same agent code to pivot between diverse industries (FinTech, Logistics, Ride-Hailing) without re-compilation.
- **Agent Lifecycle & Registry**: Deployed a structured `tap/pkg/agent` framework separating configuration, runtime, and supervision. Implemented a centralized Component Registry mapping module strings to constructors, decoupling the Protocol framework from explicit business logic.
- **Event-Driven AI Ecosystem**: The entire architecture operates strictly on event-driven mechanics over NATS JetStream, abstracted into three structural tiers:
  - **Agents**: Event-driven LLM routines. They maintain persistent JetStream push-subscriptions (enabled by deterministic DIDs) to wake up, evaluate context, and emit proofs.
  - **Workers**: Deterministic background pipelines. They strictly react to downstream JetStream subjects to execute guaranteed data mutations without LLM inference.
  - **Tools**: Synchronous Go functions executed directly in-memory by an Agent's LLM runtime during a reasoning loop.
- **The Supervisor Loop**: Decoupled the LLM reasoning loop from the deterministic Redux state engine. The Supervisor intrinsically intercepts state mutations, enforcing strict `/_sys` mutation overrides (Entropy Filtration) prior to ledger evaluation.
- **Context Paging (`ExecWithPaging`)**: Embedded an OS-level virtual memory construct wrapping the OpenAI loop. It automatically generates ephemeral pointer maps, replacing raw document token-bloat with lightweight `local_ref` integers. The Go Kernel intercepts these integers via a deterministic `PAGE_IN` tool-calling interception, fulfilling text chunks and tracking a hard `maxPages` circuit breaker.
- **Almanac Discovery**: Decentralized agent directory over NATS. Operates as an internal cluster map allowing the Orchestrator to locate network peers by capability at runtime.
- **Semantic Vector DB Matching**: Pinecone Vector DB integration resolving similarity matching for entities against established schemas. 

### Desktop Implementations (Fignode Pro)
- **Desktop Shell & Security**: Application packaged. The authentication/RBAC flow is comprehensive, storing JWTs securely in SQLite.
- **QBO OAuth Flow**: Full Intra-app WebView Intuit OAuth handling (`pkg/qbo`), redirect ingestions, and token handoffs to the local storage.
- **Ledger Engine (`pkg/ledger`)**: Retrieves AI-classified pending transactions and submits approvals to the REST/GraphQL gateways securely.
- **Reconciliation Gateway (`pkg/torogateway`)**: Executes period-based sync verifications between Intuit endpoints and the Local amounts.

### Mobile Implementations (Fignode App)
- **Core Gestures:** Swipe-deck UI indexing transactions (Approve AI, Reclassify, Skip, Details).
- **Offline Simulator:** Built-in dummy models mimic vendor behaviours, enabling offline UI trial runs.
- **QBO Entry Layer (`useQBOAuth.js`)**: React Native hook initiating local QBO connections surviving background suspensions.

---

### UI Bridging
- **Dynamic UX State WebSockets:** The Go API Gateway proxies Redux `RFC 6902` JSON patches from JetStream down active WebSocket bindings. The React frontend hydrates this payload to render real-time worker execution without database polling loops.
- **Fignode Taxonomy Broadcasting:** Deployed `fignode.EnsureCompanyContext` and `EnsureVendorContext` alongside the WebSocket broadcasters securely injected with OpenAI API Keys. This pipelines high-fidelity Gamification metadata (Mindset hints, Business Models, Industry icons) directly into the UX arrays upon synchronization.
- **AI Agent Composer (Desktop):** Chat interface enabling operators to deploy and configure specialized AI agents.
- **Agent Chat Interface (Desktop):** React interface processes dual-streaming (PCM audio / NDJSON text) from the Go sidecar.

---

## 4. Phase II: Enterprise Hardening & Scale.

 funding will immediately unblock replacing robust local logic with high-throughput production data pipes. The following milestones represent the roadmap:

### 1. Live State Broadcasting (WebSockets)
- **Fignode Push Hydration**: Hardened the WebSocket pipeline to bind transient Core NATS subscriptions dynamically. This bypasses structural API bottlenecks to stream Fignode JSON arrays instantaneously to the React Native frontend the precise millisecond asynchronous JetStream accounting reconciliations complete.
- **Synchronized Connection Lifecycles**: Overcame native Go standard-library constraints by natively binding NATS JetStream polling contexts directly to the blocking `ServeWS` HTTP handler. This ensures that transient JetStream proof polling is elegantly terminated upon WebSocket TCP disconnect or server shutdown, strictly enforcing the daemon's fail-fast graceful exit mechanics.
- **Mobile Notifications**: Current WebSockets notify the Fignode Apps, but Background Service Push architecture must be spun up to alert Native applications of refreshed internal queues or new QBO transaction batches instantly upon OS suspend.
- **The "Hound Agent"**: Implementing the Twilio/webhook integration (`dispatchHoundAgent` stub) to message/chase a third-party client regarding flagged transactions.

### 2. Desktop Application Advancements
- **End-to-End Invoice Creation**: Moving beyond purely reviewing pending transactions to *authoring* manual bills/invoices directly into the Desktop UI for upstream ERP staging.
- **Multi-Tenant / Multi-Org State**: Switching gracefully between CPA clients on the Desktop app requires a hardened cache-clearing matrix in SQLite.
- **Advanced Error UIs**: Refining Wails bindings to render elegant offline, network timeout, or ledger discrepancy failures to the user.

### 3. QA & Infrastructure
- **Composability Audit**: Conducted a formal architectural review of the Event-Driven Control Plane. Identified and mapped the transition path from linear execution to infinite DAG-based workflows and hierarchical conversation tracking.
- **Comprehensive E2E Test Suite**: Initiated the development of integration rails mapping the full TAP envelope lifecycle (Ingestion -> Bus -> Orchestrator -> Agent -> Proof -> DB).
- **Automated Deployment CI/CD**: Establishing Apple TestFlight, Google Play, and DMG/AppImage distribution paths across GitHub Actions.

---


## Conclusion: The Inevitable Standard
Toro is not an AI wrapper; it is a fundamental correction to enterprise AI architecture. By combining an event-sourced Go Kernel with strict Micrion tolling and a decentralized Agentic Protocol, we have mitigated the high-IO bottlenecks and hallucination risks that paralyze legacy accounting software. 

The Fignode clients prove that we can seamlessly bridge mathematically rigorous backend state with visually stunning, real-time human oversight. 
