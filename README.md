# Toro Platform - Developer Documentation

> Banking-grade financial intelligence platform with fault-tolerant webhook architecture

## Table of Contents

- [Architecture Overview](#architecture-overview)
- [Technology Stack](#technology-stack)
- [Getting Started](#getting-started)
- [Project Structure](#project-structure)
- [Core Systems](#core-systems)
- [Development Workflow](#development-workflow)
- [API Documentation](#api-documentation)
- [Testing](#testing)
- [Deployment](#deployment)
- [Troubleshooting](#troubleshooting)

## Architecture Overview

Toro uses a "Thick Go, Thin Python" architecture. Go handles the high-speed IO, while Python (Flask) acts as the intelligent "Refinery" for data analytics and normalization.

```mermaid
  graph TD
    subgraph External ["EXTERNAL WORLD"]
        Providers["Stripe, Plaid, QBO"]
        Users["Mobile App / WhatsApp"]
    end

    subgraph Ingress ["THE GATE (Go)"]
        GoGate["cmd/gate"]
        note1["Port 8080<br/>Stateless<br/>Http -> NATS"]
    end

    subgraph Vault ["THE VAULT"]
        NATS["NATS JetStream"]
        note2["Events & Task Queue"]
    end

    subgraph Intelligence ["THE AGENT CLUSTER"]
        GoBrain["THE PROTOCOL (Go)"]
        note3["cmd/protocol<br/>Port 8081<br/>State Machine<br/>Calls LLMs"]
        
        PythonWorker["THE HANDS (Python)"]
        note4["Sidecar<br/>OCR / Pandas<br/>gRPC over Unix Socket"]
    end

    subgraph Connectors ["THE SYNC ENGINE (Go)"]
        GoSync["cmd/sync"]
        note5["Background Worker<br/>Rate Limiters<br/>Token Refresh<br/>Plaid/QBO Clients"]
    end

    subgraph Egress ["THE CANNON"]
        Svix["Svix Server"]
    end

    %% Flow
    Providers -->|Webhooks| GoGate
    Users -->|WebSocket Audio| GoGate
    
    GoGate -->|1. Publish Input| NATS
    
    NATS -->|2. Subscribe| GoBrain
    
    GoBrain -->|3a. Reason| LLM(("LLMs"))
    GoBrain <-->|3b. Heavy Calc| PythonWorker
    
    %% The New Path
    GoBrain -.->|3c. Request Data| NATS
    NATS -.->|4. Fetch| GoSync
    GoSync <-->|5. API Call| Providers
    GoSync -.->|6. Data Ready| NATS
    
    GoBrain -->|7. Dispatch Result| Svix
    Svix -->|Webhook| CustomerApp["Customer App"]
  ```
### Design Principles

1. **Go for Speed**: Ingress and Routing are handled by Go.
2. **Python for Smarts**: Flask handles Data Analytics, LLM Logic, and Normalization.
3. **Vault First**: All events persist to NATS JetStream before any processing.
4. **Atomic Handoff**: Workers only ACK NATS after successful processing/dispatch.
5. **Isolated Infrastructure**: Svix runs as a sidecar with its own Redis.

## Technology Stack

### Backend

- **Go 1.22+** - High-performance Ingress ("The Gate")
  - `pgx/v5` - Binary PostgreSQL driver
  - `ristretto` - High-performance memory cache
  - `nats.go` - JetStream Client

- **Python (Flask)** - Analytics & Normalization ("The Refinery")
  - `Flask 3.0` - Lightweight microframework
  - `Graphene-Python` - GraphQL API
  - `SQLAlchemy` - Database ORM (for complex analytics queries)
  - `Pydantic` - Data validation & Schema definition

- **Storage & Messaging**
  - **PostgreSQL 15** - Unified Storage (Tenants, Transactions, Analytics)
  - **NATS JetStream** - The Event Log (Source of Truth)
  - **Svix** - Webhook Dispatch
  - **Redis** - Hot state & Idempotency keys

### Frontend

- **React 18** - UI Library
- **TypeScript** - Type Safety
- **Redux Toolkit** - State Management
- **Vite** - Build Tool

### Infrastructure

- **Docker Compose** - Orchestration
- **Nginx** - Reverse Proxy & WebSocket Termination

## Getting Started

### Prerequisites

- Docker & Docker Compose
- Python 3.12+
- Go 1.22+
- Node.js 18+

### Initial Setup

1. **Clone & Configure**
   ```bash
   git clone <repo>
   cd usetoro
   cp .env.example .env
   ```

2. **Start Infrastructure**
   ```bash
   make up  # Starts Postgres, NATS, Redis, Svix
   ```

3. **Initialize Database**
   ```bash
   make migrate  # Runs Goose migrations
   ```

4. **Start Services**
   ```bash
   make dev  # Starts Go Gate + Flask Refinery
   ```

### Access

- **Frontend**: [http://localhost:3000](http://localhost:3000)
- **GraphQL API**: [http://localhost:8000/graphql](http://localhost:8000/graphql)
- **Svix Dashboard**: [http://localhost:8071](http://localhost:8071)

## Project Structure

```text
usetoro/
├── go-gate/                # The Ingress (Go)
│   ├── cmd/main.go         # Entry point
│   ├── internal/
│   │   ├── api/            # HTTP Handlers
│   │   ├── ingest/         # NATS Publishers
│   │   └── security/       # Signature Verification
│   └── go.mod
│
├── python-refinery/        # The Brain (Flask)
│   ├── app.py              # Flask Entry point
│   ├── worker.py           # NATS Consumer Entry point
│   ├── core/
│   │   ├── models.py       # SQLAlchemy Models
│   │   ├── schema.py       # GraphQL Schema
│   │   └── database.py     # DB Connection
│   ├── normalization/      # Provider Logic
│   │   ├── stripe.py
│   │   └── plaid.py
│   └── bridge/             # NATS -> Svix Logic
│
├── sql/                    # Database Schema
│   └── schema/             # Goose Migrations
│
├── container/              # Docker Configs
│   ├── docker-compose.yml
│   └── nginx/
│
└── Makefile                # Command shortcuts
```

## Core Systems

### 1. Go Ingress ("The Gate")

Handles the "Firehose" of incoming webhooks.

- **Port**: 8080
- **Responsibility**: Auth -> Cache Check -> NATS Publish.
- **Zero Logic**: It does not parse JSON deeply. It treats payloads as `[]byte`.

### 2. NATS JetStream ("The Vault")

Immutable ledger of all raw events.

- **Stream**: `TORO_EVENTS`
- **Subjects**: `raw.{provider}.{tenant_id}`

### 3. Python Refinery ("The Hands")

Consumes from NATS and applies business logic.

- **Worker Process**: `python worker.py`
- **Responsibility**:
  - Read raw JSON.
  - Fetch "BYOK" secrets from DB.
  - Call Provider API (e.g. Plaid) for extra data.
  - Normalize to ToroTransaction.
  - Dispatch to Svix.

### 4. Flask API ("The Read Layer")

Serves the Frontend.

- **Port**: 5000 (Proxied to 80)
- **Tech**: Flask + Graphene.
- **Why Flask?**: We need Python's data libraries (Pandas/NumPy) for the "Analytics" dashboard (Burn rate, Cash flow forecasting) which are hard to write in Go.

## API Documentation (GraphQL)

Since we removed Django, we use Graphene-Python directly with Flask.

**Endpoint**: `/graphql`

### Queries (Analytics)

```graphql
query GetCashflow {
  analytics(tenantId: "uuid") {
    burnRate
    runwayDays
    forecast(days: 30) {
      date
      predictedBalance
    }
  }
}
```

### Mutations (Control)

```graphql
mutation DispatchTest {
  triggerWebhook(
    eventType: "transaction.created",
    payload: "{\"amount\": 100}"
  ) {
    success
    messageId
  }
}
```

## Deployment & Performance

### Why this is faster than Django

1. **No Middleware Bloat**: Flask is barebones. We only add what we need.
2. **Go Ingress**: The heavy HTTP lifting is done by Go, not Gunicorn/uWSGI.
3. **Async Workers**: The Python `worker.py` can run essentially as a script without the overhead of a web server framework.

### Production Dockerfile (Python)

```dockerfile
FROM python:3.11-slim-bookworm

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY . .

# Run the API and the Worker side-by-side (or separate containers)
CMD ["gunicorn", "-w", "4", "-b", "0.0.0.0:5000", "app:app"]
```
