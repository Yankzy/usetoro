# Toro Agent Protocol (TAP) API Documentation

**Version:** 1.0.0  
**Status:** Active  
**Directory:** `tap/api/`

## Introduction

The **Toro Agent Protocol (TAP)** is a distributed communication standard designed to facilitate autonomous interaction between agents in a decentralized network. 

Because TAP is language-agnostic, agents can be written in **Go, Python, Rust, JavaScript**, or any other language capable of speaking **JSON over NATS**. The definitions in this `api/` directory serve as the "Law" of the network—the strict schemas that all agents must obey to communicate successfully.

This document provides a verbose reference for all schema definitions, field requirements, and interaction flows.

---

## 1. The Envelope (`envelope.json`)

**Schema File:** [`api/json-schema/v1/envelope.json`](./json-schema/v1/envelope.json)

The **Envelope** is the fundamental packet of the network. Every single message transmitted over the NATS layer **MUST** validate against this schema. It wraps the business logic ("Body") with the necessary metadata for routing, security, and intent.

### Schema Definition

```json
{
  "id": "uuid-v4-string",
  "ts": "2023-10-27T10:00:00Z",
  "src": "did:toro:sender_agent_id",
  "dst": "did:toro:receiver_agent_id",
  "perf": "cfp",
  "cid": "conversation-correlation-id",
  "body": { ... },
  "sig": "base64_signature"
}
```

### Field Reference

| Field | Type | Required | Description |
| :--- | :--- | :--- | :--- |
| **`id`** | `string` (UUID) | **Yes** | A unique identifier for this specific message instance. Used for de-duplication and logging. |
| **`ts`** | `string` (ISO 8601) | **Yes** | The timestamp of creation. Used to prevent **Replay Attacks** (nodes can reject messages older than separate threshold). |
| **`src`** | `string` (DID) | **Yes** | The **Decentralized Identifier** of the sender. Must match the regex `^did:toro:.*$`. |
| **`dst`** | `string` (DID) | No | The DID of the intended recipient. If omitted, it implies a **Broadcast** to a public topic. |
| **`perf`** | `string` (Enum) | **Yes** | The **Performative** (Speech Act). It tells the receiver *how* to interpret the `body`. (See below). |
| **`cid`** | `string` | No | **Correlation ID**. Used to thread messages together into a Conversation. A Reply MUST use the same `cid` as the Request. |
| **`body`** | `object` | **Yes** | The payload. The structure of this object depends on the `perf` and the business context (e.g., a `Task` or `Contract`). |
| **`sig`** | `string` | **Yes** | A cryptographic signature of the `body`. This proves that `src` is the true author of the message. |

### Valid Performatives (`perf`)

The `perf` field dictates the intent of the message.

* **`cfp`** ("Call For Proposal"): A broadcaster is looking for workers. Body usually contains a `Task`.
* **`propose`**: A worker offers to perform the task. Body contains terms (price, eta).
* **`accept-proposal`**: The original broadcaster agrees to the terms.
* **`reject-proposal`**: The broadcaster declines the offer.
* **`inform`**: General status update (e.g., "I started driving").
* **`query-ref`**: Requesting information (e.g., "Where is the package?").
* **`request`**: Generic request for action.
* **`refuse`**: Explicit refusal to perform a requested action.
* **`failure`**: Notification that an error occurred.

---

## 2. Almanac Registration (`almanac.json`)

**Schema File:** [`api/json-schema/v1/almanac.json`](./json-schema/v1/almanac.json)

Before an agent can participate, it must register with the **Almanac** (the network registry). This schema defines the payload strictly required for registration.

### Schema Definition

```json
{
  "did": "did:toro:trucker_01",
  "endpoints": ["agents.trucker_01.inbox"],
  "capabilities": [
    {
      "type": "logistics.trucking",
      "meta": { "max_weight": 5000 }
    }
  ],
  "expiry": "2023-10-27T12:00:00Z"
}
```

### Field Reference

| Field | Type | Required | Description |
| :--- | :--- | :--- | :--- |
| **`did`** | `string` | **Yes** | The public identity of the agent registering. |
| **`endpoints`** | `[]string` | **Yes** | A list of NATS subjects where this agent listens for direct messages (Personal Inbox). |
| **`capabilities`** | `[]object` | **Yes** | A list of tags describing what the agent can do. Used for discovery. |
| **`capabilities.type`**| `string` | **Yes** | The capability namespace (e.g., `logistics.trucking`, `accounting.audit`). |
| **`expiry`** | `string` (ISO 8601)| **Yes** | The time when this registration entry becomes invalid. Agents must re-register (heartbeat) before this time. |
| **`signature`** | `string` | No | Optional signature field if the registration acts as a self-attested claim. |

---

## 3. Business Primitives (`primitives.json`)

**Schema File:** [`api/json-schema/v1/primitives.json`](./json-schema/v1/primitives.json)

These are the standard objects that inhabit the `body` of an Envelope during the lifecycle of a job, ensuring that "Task" and "Contract" mean the same thing to everyone.

### A. Task (`task`)
Represents a unit of work to be done. Usually broadcast with `perf: cfp`.

| Field | Type | Validation | Description |
| :--- | :--- | :--- | :--- |
| **`id`** | `string` | - | Unique ID for the task. |
| **`domain`** | `string` | - | The industry domain (e.g., `logistics`, `data`). |
| **`complexity`** | `integer` | `1 <= x <= 10` | Abstract rating of difficulty. |
| **`reward`** | `integer` | - | The offered payment amount. |
| **`currency`** | `string` | - | e.g., "USD", "TORO". |
| **`payload`** | `object` | - | The raw data required to do the work (e.g., coordinates, image URL). |
| **`expires_at`** | `integer` | - | Unix timestamp when this opportunity vanishes. |

### B. Contract (`contract`)
Represents an agreed-upon engagement between two parties.

| Field | Type | Options | Description |
| :--- | :--- | :--- | :--- |
| **`id`** | `string` | - | Unique Contract ID. |
| **`status`** | `string` | `DRAFT`, `LOCKED`, `SETTLED`, `DISPUTED` | The current state of the agreement. |
| **`initiator_did`** | `string` | - | The party requesting work. |
| **`acceptor_did`** | `string` | - | The party performing work. |
| **`terms`** | `object` | - | A snapshot of the final agreed terms (Price, Deadline, etc.). |
| **`signatures`** | `object` | - | A map of `{ "did": "signature" }`. Both parties MUST sign for it to be `LOCKED`. |

### C. Proof (`proof`)
Evidence submitted by the worker to claim the reward.

| Field | Type | Description |
| :--- | :--- | :--- |
| **`task_id`** | `string` | The ID of the task/contract being fulfilled. |
| **`type`** | `string` | The classification of proof (e.g. `gps_trace`, `hash_preimage`). |
| **`ts`** | `integer` | Unix timestamp of completion. |
| **`data`** | `object` | The actual result (e.g., `{"lat": 40.7, "lng": -74.0}`). |
| **`sig`** | `string` | Signature of the worker, certifying this proof is authentic. |

---

## 4. Interaction Flow & Examples

Here is a complete lifecycle of a job execution, showing the raw JSON messages exchanged.

### Step 1: Broadcast (Call For Proposal)
**Sender (`did:toro:generator`)** posts a job to the public topic `tasks.logistics`.

```json
// Topic: tasks.logistics
{
  "id": "msg_001",
  "ts": "2023-11-01T10:00:00Z",
  "src": "did:toro:generator",
  "perf": "cfp",
  "cid": "conv_888",
  "body": {
    "id": "task_alpha",
    "domain": "logistics",
    "complexity": 5,
    "reward": 100,
    "currency": "USD",
    "payload": {
      "pickup": "New York",
      "dropoff": "Boston"
    },
    "expires_at": 1698883200
  },
  "sig": "abc123signature..."
}
```

### Step 2: Proposal
**Worker (`did:toro:trucker`)** sees the job, calculates they can do it, and sends a direct proposal to the Generator's inbox.

```json
// Topic: agents.did:toro:generator.inbox
{
  "id": "msg_002",
  "ts": "2023-11-01T10:00:05Z",
  "src": "did:toro:trucker",
  "dst": "did:toro:generator",
  "perf": "propose",
  "cid": "conv_888",  // Matches the CFP's cid
  "body": {
    "price": 100,
    "eta": "4 hours",
    "vehicle": "Van"
  },
  "sig": "worker_sig_xyz..."
}
```

### Step 3: Acceptance & Contract Generation
**Generator** accepts the proposal and drafts a contract.

```json
// Topic: agents.did:toro:trucker.inbox
{
  "id": "msg_003",
  "src": "did:toro:generator",
  "dst": "did:toro:trucker",
  "perf": "accept-proposal",
  "cid": "conv_888",
  "body": {
    "id": "contract_z99",
    "status": "DRAFT",
    "initiator_did": "did:toro:generator",
    "acceptor_did": "did:toro:trucker",
    "terms": {
      "task_id": "task_alpha",
      "price": 100,
      "eta": "4 hours"
    },
    "terms_hash": "sha256_of_terms_json",
    "signatures": {
      "did:toro:generator": "generator_sig_on_hash"
    },
    "created_at": "2023-11-01T10:01:00Z"
  },
  "sig": "generator_msg_sig..."
}
```

### Step 4: Worker Signature & Lock
The Worker signs the contract hash and submits it to the **Hive Engine** to be finalized.

```json
// Topic: contracts.request
{
  "id": "contract_z99",
  "status": "LOCKED",  // Status update request
  "signatures": {
    "did:toro:generator": "generator_sig_on_hash",
    "did:toro:trucker": "new_worker_signature_on_hash"
  },
  // ... other fields ...
}
```

### Step 5: Proof Submission
After doing the work, the Worker submits a Proof.

```json
// Topic: proof.submit
{
  "task_id": "contract_z99",
  "type": "delivery_confirmation",
  "ts": 1698897600,
  "data": {
    "image_url": "https://toro.net/proofs/img_1.jpg",
    "recipient_signature": "recipient_signed_pad"
  },
  "sig": "worker_proof_sig..."
}
```

---

## 5. Standard Topics (NATS Subjects)

To ensure agents can find each other, TAP uses a strict topic taxonomy.

| Topic Pattern | Purpose | Payload Schema |
| :--- | :--- | :--- |
| **`almanac.register`** | Agent Registration (Heartbeat) | `almanac.json` |
| **`almanac.query`** | Looking up agents by capability | `{ "capability": "..." }` |
| **`tasks.{domain}.{complexity}.>`** | Broadcasts for new work. | `envelope.json` (Body: `task`) |
| **`agents.{did}.inbox`** | Direct messages to a specific agent. | `envelope.json` |
| **`contracts.request`** | Submitting signed contracts to Hive for locking. | `contract` (JSON) |
| **`proof.submit`** | Submitting final proofs for settlement. | `proof` (JSON) |

* **Wildcards**: Agents usually subscribe to `tasks.logistics.>` to hear about all logistics jobs, or `tasks.logistics.1.>` for simple ones.

---

## 6. Tooling & AsyncAPI

For automated code generation and validation, a formal machine-readable definition is available:

* **AsyncAPI**: [`api/asyncapi.yaml`](./asyncapi.yaml)

You can use tools like the [AsyncAPI Generator](https://www.asyncapi.com/tools/generator) to scaffold agent code in Java, Node.js, Python, or Go using this definition.
