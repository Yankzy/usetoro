# AgentHub Primitives: Codebase Audit

This document audits the current state of the *usetoro* codebase against the 5 foundational AgentHub primitives outlined in the architecture document.

## 1. Agent Registry (Analogous to AWS IAM + Service Catalog)
* **Purpose**: Single source of truth for discovering, verifying, and managing agents.*
**Status**: 🟢  **Built (Almanac Server)**
- **Analysis**: The `almanac-server` (`cmd/protocol/almanac-server`) serves exactly this purpose. It listens on NATS for `almanac.register` and `almanac.query` events. Crucially, it acts as a gatekeeper by verifying incoming Agent X.509 certificates against a Root CA before allowing them to register. Mappings of an agent's `DID` to its endpoints and `Capabilities` are stored temporarily in Redis, enforcing an expiry TTL based on the certificate and payload.
- **Action Required**: The foundation is robust. Future iterations might expose a traditional REST/GraphQL API for human-facing dashboards (e.g., viewing the registry in a web UI), as it currently operates purely over NATS for agent-to-agent discovery.

## 2. Message Gateway (Analogous to AWS API Gateway + SQS)
* **Purpose**: Secure, mediated communication channel for agents.*
**Status**: 🟢 **Built (Gate Pipeline & WebSockets)**
- **Analysis**: The `cmd/gate` service functions as the secure ingress gateway. It exposes external API endpoints (e.g., `POST /webhooks/{provider}`) and performs essential Gateway duties: validating webhook signatures (e.g., Stripe, QBO HMAC), rate-limiting (via Ristretto cache), checking auth, and then persisting the raw messages onto the scalable **NATS JetStream** (`toro-ingress` queue) for downstream agents to consume asynchronously. Additionally, `cmd/ws` provides a real-time WebSocket tunnel for client apps to interact with the system securely.
- **Action Required**: The ingestion and queuing parts are well-architected. As the AgentHub matures, this Gateway might need to explicitly support the cAIP protocol (routing general agent-to-agent JSON payloads over HTTP/WSS instead of just vendor webhooks) and enforce rate-limiting per Agent DID.

## 3. Execution Runtime (Analogous to EC2 + Lambda)
* **Purpose**: Managed environment to run agent logic.*
**Status**: 🟢 **Mostly Built / Maturing**
- **Analysis**: The `protocol-agent` daemon (`cmd/protocol/agent/main.go`) acts directly as the execution runtime. It initializes the "Supervisor" (`agent.NewSupervisor`) which manages agent lifecycle, handles hot-reloading (`SIGHUP` and `/reload` API), and provides memory tools via `tap/pkg/memory` (backed by the `agent_memory_rules` DB table) and NATS communication.
- **Action Required**: Ensure it supports containerization (scaling pods limits) and isolation (sandboxing) for arbitrary agent logic, not just pure Go supervisor threads.

## 4. Value Exchange Service (Analogous to AWS Billing + Marketplace)
* **Purpose**: Handles payments, incentives, and economic models.*
**Status**: 🟡 **Foundational Primitives Exist**
- **Analysis**: We have foundational structs in `tap/pkg/core/primitives.go` like `TaskDefinition` (containing `Reward`, `Currency`) and `Contract` (representing a signed agreement `Terms`, `Signatures`, `ContractStatus`). Furthermore, the `toro_core.transactions` table exists to hold an internal ledger for "Toro Transactions".
- **Action Required**: The APIs for escrow, resolving disputes, and processing actual clearing/settlement based on Proofs seem to need fleshing out into a dedicated service (e.g., executing the lifecycle from `ContractLocked` to `ContractSettled`).

## 5. Tool Integration Layer (Analogous to Bedrock + SDKs)
* **Purpose**: Connect external systems, data sources, and APIs.*
**Status**: 🟡 **Partially Built (Hardcoded Connectors)**
- **Analysis**: The `go/internal/connectors` directory holds integrations like `qbo.go` (QuickBooks Online), and there are webhook mechanisms for external updates. However, it looks like an internal monolithic connection rather than a standardized "plugin/SDK layer" that any marketplace agent could simply import.
- **Action Required**: Build a standardized adapter framework where tools are explicitly defined via a manifest and injected into the Execution Runtime. Abstract OAuth credential handling (currently in `toro_core.webhooks_providerconnection` and ws logic) so third-party agents can seamlessly ask for permissions to use the QBO tool.

---

### Conclusion
The codebase has excellent low-level engineering for message-passing (NATS) and supervisor runtimes, but lacks the higher-level "Marketplace" and "Control Plane" APIs (Registry, Gateway proxy, Tool manifest abstraction) needed to achieve the AWS-style ecosystem vision.
