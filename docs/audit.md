# Toro Ecosystem & Fignode Clients – Pre-Seed Technical Audit

## Executive Summary
This document provides a comprehensive technical audit of the **Toro Platform** backend infrastructure and its two primary client interfaces: **Fignode Pro** (Desktop App) and **Fignode** (Mobile App). This audit highlights the robust architecture, implemented core features, and a clear technical roadmap intended to support a $1M pre-seed fundraise.

The ecosystem utilizes a "Thick Go, Thin Python" microservices pattern around NATS JetStream for high-availability backend ingestion, coupled with dynamic, high-performance modern frontends. The underlying goal is to create a banking-grade financial intelligence system wrapped in a Tinder-style gamified UX for accountants.

---

## 1. Architecture Overview

### Backend Platform (Toro)
- **Core Microservices**: Go handles the "Gate" (stateless HTTP ingress -> NATS), "Protocol" (State Machine/Logic), "Sync" (Background workers/limiters), and "Realtime" (WebSockets) operations.
- **The Vault**: NATS JetStream persists events and acts as the immutable task queue.
- **Data & Intelligence**: PostgreSQL with the `pgx` driver is the relational source-of-truth. Python (Flask/Workers) operates the "Refinery" for OCR, heavy Pandas analytics, and orchestration via GraphQL.
- **Toro Agent Protocol (TAP)**: A custom NATS-based framework for decentralized, autonomous AI agents to negotiate tasks, execute contracts, and submit cryptographic proofs.

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
- **Ingestion & Messaging**: Fault-tolerant webhook ingestion buffers to NATS, guaranteeing atomic, isolated execution.
- **Event-Driven Workers**: Extensively refactored background workers for robust, asynchronous processing, heavily leaning into an event-driven architecture.
- **Robust Bi-directional Synchronization**: Fully functional Change Data Capture (CDC) pipeline syncing Intuit payloads to Toro and reverse-syncing reviewed categorizations back to QBO SaaS in real-time.
- **API Capabilities**: A broad GraphQL schema (`schema.graphqls`) is mapped for tenants, users, cleanup-sessions, transactions, ERP accounts, and vendors.
- **Gamified "Fignode" Service**: A standalone Go service (`cmd/fignode`) handles accounting classification, gamified interactions (badges, streaks, localized leaderboards), and email SMTP abstractions. 

### Desktop Implementations (Fignode Pro)
- **Desktop Shell & Security**: Application packaged natively. The authentication/RBAC flow is comprehensive, storing JWTs securely in SQLite.
- **QBO OAuth Flow**: Full Intra-app WebView Intuit OAuth handling (`pkg/qbo`), redirect ingestions, and token handoffs to the local storage.
- **Ledger Engine (`pkg/ledger`)**: Retrieves AI-classified pending transactions and submits approvals to the REST/GraphQL gateways securely.
- **Reconciliation Gateway (`pkg/torogateway`)**: Executes period-based sync verifications between Intuit endpoints and the Local amounts.

### Mobile Implementations (Fignode App)
- **Core Gestures:** Highly-performant swipe-deck UI capable of natively triaging transactions (Approve AI, Reclassify, Skip, Details).
- **Sandboxed Offline Simulator:** A suite of dummy models mimics complex vendor behaviours, allowing users to trial the app, trigger UI honey-pots, and build confidence thresholds without any network. 
- **QBO Entry Layer (`useQBOAuth.js`)**: An engineered React Native hook initiating QBO connections locally that survives native app-backgrounding scenarios gracefully.

---

## 3. Partially Implemented & In-Progress (The Pivot)

### Artificial Intelligence Bridging
- **Agent Chat Interface (Desktop):** The React interface (`AgentChat.tsx`) and the backend WebSockets are operational, but the dual-streaming integration (PCM audio / NDJSON text) from the Go sidecar to the UI requires final linkups to display reasoning dynamically.
- **Python Refinery Complexities:** Basic data analytics are deployed on Python, however multi-agent hierarchical triage logic is actively being ported/stabilized into the `go_sidecar_triage` structure for 250ms target latency.

### The Mobile Team Pivot
- **Schema Migrations**: The mobile app is transitioning from a "crowdsourced gig" model (with Stripe payouts) to an internal employee productivity tool (`rebranding.md`). Database tables representing bounties/withdrawals are mid-migration into internal firm scores.
- **Mobile Auth Storage**: Auth tokens are functioning but temporarily occupy vanilla `AsyncStorage`. They need hardening into Native KeyChain / SecureStorage interfaces.
- **Team Management**: Invitation APIs are stubbed but require integration logic connecting the underlying QBO tenant ID to the employee.

---

## 4. Unimplemented (Gaps & Next Steps for Investment)

Pre-seed funding will immediately unblock replacing robust local logic with high-throughput production data pipes. The following milestones represent the roadmap:

### 1. Live State Broadcasting (WebSockets)
- **Mobile Notifications**: Current WebSockets notify the Desktop app, but mobile push architecture must be spun up to alert Native applications of refreshed internal queues or new QBO transaction batches instantly.
- **The "Hound Agent"**: Implementing the Twilio/webhook integration (`dispatchHoundAgent` stub) to natively message/chase a third-party client regarding flagged transactions.

### 2. Desktop Application Advancements
- **End-to-End Invoice Creation**: Moving beyond purely reviewing pending transactions to *authoring* manual bills/invoices directly into the Desktop UI for downstream ERP staging.
- **Multi-Tenant / Multi-Org State**: Switching gracefully between CPA clients on the Desktop app requires a hardened cache-clearing matrix in SQLite.
- **Advanced Error UIs**: Refining Wails bindings to render elegant offline, network timeout, or ledger discrepancy failures to the user.

### 3. QA & Infrastructure
- **Comprehensive E2E Test Suite**: There are currently minor unit tests in the Go backend, but rigorous CI end-to-end integration mapping (React Mobile Action -> Go Gate -> NATS Bus -> Python Agent -> DB) is absent.
- **Automated Deployment CI/CD**: Establishing Apple TestFlight, Google Play, and DMG/AppImage distribution paths across GitHub Actions.

---

## Conclusion
The core Toro infrastructure is mathematically sound—utilizing an advanced, decoupled pattern (Go, NATS, GraphQL) that mitigates traditional high-IO accounting software bottlenecks. The Fignode frontend clients (Desktop and Mobile) demonstrate that complex financial interactions can be wrapped in visually stunning, highly performant UX. 

The primary technical risk has been cleared. The focus of the $1M pre-seed round is directly aimed at finalizing API interlocks between these three mature applications, establishing CI/CD automated test rails, finalizing true E2EE compliance, and executing the firm-employee rebranding pivot for commercial launch.
