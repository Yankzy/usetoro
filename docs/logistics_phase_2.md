# Product Requirements Document (PRD)

Project: Toro Logistics - Phases 2 & 3
Modules: Shipper Integrations (Ph 2) & Agentic Negotiation (Ph 3)
Status: APPROVED FOR DEVELOPMENT (Strategic Pivot)

Executive Summary

The Strategic Pivot: We are bypassing the legacy factoring and Notice of Assignment (NOA) strategy. Having aggregated tens of thousands of compliant carriers in Phase 1 (Compliance OS), we will now connect directly to Enterprise Shippers.
The Mechanism: Shippers fund a "Smart Escrow." Toro AI Agents (representing Carriers) negotiate directly with Shipper APIs over our NATS JetStream event bus. When the truck hits the delivery geofence, the Escrow instantly releases the funds.
The Result: We cure payment latency without taking on loan risk, and the human freight broker becomes an obsolete, optional layer.

PHASE 2: SHIPPER DEMAND & SMART ESCROW

The Goal: Open the Toro Protocol to the demand side (Shippers: Target, P&G, Walmart, etc.). We provide them with instant capacity and live tracking, in exchange for them funding the freight cost upfront into a trustless escrow.

Epic 6: Shipper API Gateways

Objective: Allow enterprise shippers to bypass brokers and inject freight directly into the Toro Protocol without leaving their existing software.

REQ 6.1 - TMS Connectors: Build standard API ingestors for enterprise Transportation Management Systems (SAP, Oracle, BlueYonder, MercuryGate).

REQ 6.2 - The "Load Broadcast" Event: When a shipper pushes a load via API, Toro translates it into a standardized FreightContractProposed event and publishes it to the NATS JetStream bus.

REQ 6.3 - Live Telemetry Webhooks: Provide shippers with a dedicated API endpoint streaming real-time ELD GPS data (harvested from the Carrier in Phase 1) for their specific active loads. This eliminates all "Where is my truck?" check-calls.

Epic 7: Deterministic Smart Escrow

Objective: Eliminate the need for factoring companies by curing the payment latency. Counterparty risk drops to zero.

REQ 7.1 - Escrow Sub-Ledgers: When a shipper awards a load, they must fund a Toro Smart Escrow sub-ledger via ACH/Wire API (e.g., Stripe Treasury or direct BaaS integration).

REQ 7.2 - Contract Lock: The freight contract state mutates to LOCKED only when the escrow sub-ledger confirms the balance matches the negotiated rate.

REQ 7.3 - Escrow Transparency: The Carrier's UI displays a "Funds Secured" badge, proving the money is locked in escrow before they even start their engine.

PHASE 3: THE AGENTIC MARKETPLACE & SETTLEMENT

The Goal: High-Frequency Trading for physical freight. We replace the human broker with M2M (Machine-to-Machine) algorithmic negotiation, maximizing yield for the carrier and minimizing cost for the shipper.

Epic 8: High-Frequency Agentic Negotiation

Objective: The core M2M workflow. Truckers' AI agents haggle with the Shipper API on the NATS JetStream bus in milliseconds.

REQ 8.1 - Carrier Yield Parameters: The Carrier inputs baseline quantitative rules into their Toro app: Minimum profit margin (e.g., 18%), preferred lanes, maximum deadhead miles, and minimum rate-per-mile.

REQ 8.2 - Spatial & Margin Math: When a FreightContractProposed event hits NATS, the Carrier Agent calculates real-time distance from the truck's current ELD location to the pickup (Deadhead), plus live diesel costs (from Phase 1 IFTA engine) and tolls.

REQ 8.3 - The Bidding Loop: If the math clears the Carrier's yield parameters, the Carrier Agent instantly submits a cryptographically signed blind bid to the NATS bus.

REQ 8.4 - Algorithmic Award: The Shipper's logic evaluates bids based on 1) Price, 2) Carrier Safety Score (from Phase 1), and 3) Proximity/ETA. The contract is awarded in milliseconds.

Epic 9: Zero-Touch Execution & Geofenced Settlement

Objective: The physical world catches up to the digital contract. Settlement happens deterministically without human invoicing.

REQ 9.1 - Autonomous Dispatch: Upon contract award, the route coordinates and pickup numbers are pushed directly via API into the driver's ELD or navigation tablet. State mutates to IN_TRANSIT.

REQ 9.2 - Geofenced Proof of Delivery (POD): The Toro Agent monitors the truck's ELD telemetry. When the GPS registers inside the destination geofence for > 30 minutes, it triggers a DeliveryConfirmed event.

REQ 9.3 - Instant Settlement: The DeliveryConfirmed event acts as the cryptographic key that unlocks the Smart Escrow (Epic 7). The Toro ledger automatically debits the Escrow and credits the Carrier's Wallet.

REQ 9.4 - Zero-Click Invoicing: The system auto-generates a perfectly reconciled receipt and pushes it back to the Shipper's ERP.

Technical & Architectural Considerations

NATS JetStream Concurrency: Epic 8 (Agentic Negotiation) requires massive throughput. JetStream must be configured for strict ExactlyOnce delivery semantics to prevent race conditions where two carriers are awarded the same load.

State Management Security: The contract state machine flows strictly as: BROADCAST -> BIDDING -> AWARDED -> ESCROW_LOCKED -> IN_TRANSIT -> DELIVERED -> SETTLED. All mutations must occur via the Postgres CDC pipeline to ensure the immutable ledger remains the single source of truth.

Broker Neutrality (Optionality): We do not block legacy brokers. If a legacy broker wants to act as a "Shipper" and fund the Smart Escrow via API, the system accepts them. However, they will naturally be priced out of the market by direct Shipper-to-Carrier algorithms.
