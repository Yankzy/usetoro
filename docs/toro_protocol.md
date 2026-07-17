# Toro Agent Protocol (TAP)

Toro Agent Protocol (TAP) is the universal standard for autonomous economic agents, providing the foundational infrastructure for an agentic e-commerce platform. It is a distributed communication protocol designed to enable autonomous agents to discover each other, negotiate contracts, execute tasks, and submit cryptographically verifiable proofs over a decentralized messaging infrastructure.

## Platform Vision

TAP aims to solve the limitations of traditional multi-agent systems—which are often tightly coupled, centralized, and hard to scale or audit—by providing:
- **Language-agnostic JSON schemas** that allow agents written in any language to participate.
- **Decentralized identities (DIDs)** utilizing Ed25519 cryptography for secure and verifiable interactions.
- **NATS-native routing** leveraging NATS JetStream for massive scalability and high-throughput messaging.
- **Contract state machines** with cryptographic proof requirements to ensure trusted execution across organizational boundaries.

The ultimate goal is to provide the primitives necessary to create dynamic marketplaces of autonomous workers, from logistics (like trucking agents) to specialized digital tasks (like accounting bots).

## Core Capabilities (What is Done)

The TAP framework currently supports the following key capabilities enabling agent-to-agent communication:

### 1. Agent Discovery and Registry
Agents can register their capabilities and endpoints with a registry service, allowing other agents to query and discover them based on specific domain needs (e.g., "I can do logistics").
- **Almanac Server**: Acts as the agent registry and discovery service. It manages agent heartbeats and registrations using Redis, indexing capabilities for fast, case-insensitive, and metadata-filtered lookups.

### 2. Standardized Communication
Every interaction between agents is structured, secure, and clear in intent.
- **Envelopes**: Every message in TAP is wrapped in a standardized envelope format containing the sender/receiver DIDs, correlation IDs for conversations, the performative intent, and cryptographic signatures of the payload.
- **Performatives (Speech Acts)**: TAP implements a rich set of standardized message intents, such as `cfp` (Call For Proposal), `propose` (Bid/Offer), `accept-proposal`, `inform` (Status Updates), and `query-ref`.

### 3. Cryptographic Identity and Trust
Every agent in the TAP ecosystem operates under a verifiable identity without relying on a central authority.
- **Decentralized Identifiers (DIDs)**: Agents generate an Ed25519 key pair, where the public key derives the DID and the private key is used to sign message envelopes. This ensures message authenticity and non-repudiation.

### 4. Smart Contracts and Proofs
TAP goes beyond simple messaging by enforcing stateful, binding agreements between agents.
- **Hive Engine (Contract Validator)**: A state machine that manages the lifecycle of a contract (`DRAFT` → `LOCKED` → `SETTLED` / `DISPUTED`). It validates dual signatures (requiring both parties to agree) and processes settlement requests.
- **Oracle Gateway**: An external verification gateway that validates proofs (e.g., API responses, GPS traces, or classification results) submitted against a contract before settlement.
- **Business Primitives**: The system uses structured data for `Tasks` (the work specification), `Contracts` (the binding agreement), and `Proofs` (evidence of completion).

### 5. Transport and Routing
- **NATS Topics**: Agents communicate via structured NATS subjects (e.g., `almanac.register`, `tasks.{domain}.{complexity}.>`). Wildcard subscriptions allow flexible routing, enabling agents to listen to broad domains or specific task complexities.

## Architecture Structure

The TAP ecosystem consists of three core services and a robust SDK:
- **Agent Network**: Autonomous agents (e.g., Trucker, Accountant) acting as broadcasters or workers.
- **Almanac Server**: For agent registry and discovery.
- **Hive Engine**: For contract validation and state management.
- **Oracle Gateway**: For validating external proofs.
- **SDK (`pkg/`)**: A Go library providing all necessary primitives (Envelope, Task, Contract, Proof) and utilities (Identity, Transport) to build and integrate agents rapidly.

## Summary
TAP forms the backbone of Toro's agentic platform. By combining decentralized identities, structured speech acts, and cryptographically secured contracts over a high-performance message bus, TAP makes it possible to build a scalable, trustless marketplace where economic agents can autonomously negotiate and conduct business.
