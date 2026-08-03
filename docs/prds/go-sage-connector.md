# Technical PRD: Toro Go-Sage Local Connector (`toro-sage-connector`)

## Status

- [x] Draft
- [ ] Review
- [ ] Approved
- [ ] Executed

**Document Version:** 1.0  
**Target Architecture:** Golang 1.22+, Microsoft SQL Server (`go-mssqldb`), NATS JetStream / WebSocket (WSS), Windows Service Daemon  
**Target ERP:** Sage 100c / Sage 100 SQL Edition & Legacy `.MAE` instances  
**Core Objective:** Build a lightweight, high-performance local agent that runs on-premise alongside Sage 100 to handle schema introspection, bi-directional sync of master accounting records (`F_COMPTEG`, `F_COMPTET`), and safe posting of 4-Way Matched transactions into Sage.

---

## 1. Executive Summary & Strategy

The `toro-sage-connector` acts as the **bridge between Toro’s Autonomous Semantic Engine (ASE) in the cloud and local on-premise Sage 100 installations** at Moroccan accounting firms (*Fiduciaires*) and B2B enterprises.

Instead of requiring accountants to migrate off Sage on day one, the desktop app is positioned as **Toro Data Entry AI for Sage**:
1. **Introspects** the client's custom Sage Chart of Accounts and User-Defined Fields (UDFs).
2. **Stateful Task-Based Data Entry**: Users create a **Task** (e.g. *"July 2026 Bank Reconciliation - Client Alpha"*) and drop initial documents (PDF Bank Statements, Invoices).
3. **Asynchronous Hydration**: The Task remains **OPEN** in Toro Cloud until all 4-way matching rules and confidence thresholds ($C \ge 0.98$) are satisfied. If documents are missing, users simply drop incoming BCs/BLs into the existing Task.
4. **Auto-Close & Sage Sync**: Once the DAG completes and satisfies all requirements, Toro Cloud **automatically closes the Task** and posts balanced entries directly into Sage.

---

## 2. Desktop UX & Stateful Task Lifecycle

```
[ User Action: Open Task & Drag/Drop Documents ]
                       │
                       ▼
┌─────────────────────────────────────────────────────────────┐
│                 TASK STATE: OPEN / PROCESSING               │
│                                                             │
│ 1. OCR Extraction (BC / BL / Facture / Bank Statement)      │
│ 2. DAG Execution (pcm_bank_reconciliation.yml)              │
└──────────────────────┬──────────────────────────────────────┘
                       │
                       ▼
            Is Confidence C >= 0.98 
           & 4-Way Match Complete?
               /              \
         YES  /                \  NO (Missing Docs / Ambiguous)
             /                  \
            ▼                    ▼
┌───────────────────────┐  ┌─────────────────────────────────┐
│ TASK STATE: CLOSED    │  │ TASK STATE: HOLD_MISSING_DOCS   │
│                       │  │                                 │
│ • Write to Sage 100   │  │ • Task remains OPEN in App      │
│ • Audit UDFs Injected │  │ • User drops new BL/Facture into│
│ • Lock Period Entry   │  │   same Task to Resume Agent     │
└───────────────────────┘  └─────────────────────────────────┘
```

### Key Workflow Rules:
- **Persistent Hydrated State**: A task serialized in `fignode.order_reconciliations` stores incomplete match chains.
- **Drag-and-Drop Resume**: Dropping a file onto an open Task calls `DomainTool.ResumeAgent()`, attaching the new document to the existing agent context and resuming the DAG.
- **Automated Closure**: Accountants never manually "close" tasks. Toro Cloud closes the Task only when the DAG execution reaches the terminal `export` node and validates Sage entry creation.

---

## 2. System Architecture

The connector runs as a lightweight, low-footprint background Windows Service (`toro-sage-agent.exe`) on the client's local server hosting Sage SQL Server.

```
 ┌───────────────────────────────────────────────────────────┐
 │                       TORO CLOUD                          │
 │  Autonomous Semantic Engine (ASE) + NATS JetStream        │
 └─────────────────────────────┬─────────────────────────────┘
                               │ Encrypted WebSocket (WSS)
                               ▼
 ┌───────────────────────────────────────────────────────────┐
 │               ON-PREMISE LOCAL SERVER / HOST              │
 │                                                           │
 │  ┌─────────────────────────────────────────────────────┐  │
 │  │        toro-sage-connector (Go Daemon)              │  │
 │  └──────┬───────────────────┬───────────────────┬──────┘  │
 │         │                   │                   │         │
 │  Schema Introspector   SQL Driver         .PNM Generator  │
 │  (UDF / Metadata)      (Read/Direct)      (Native Import) │
 │         │                   │                   │         │
 └─────────┴───────────────────┴───────────────────┴─────────┘
                               │
                               ▼
                ┌─────────────────────────────┐
                │  Sage 100 SQL Server DB     │
                │ (F_COMPTEG, F_COMPTET, etc) │
                └─────────────────────────────┘
```

### Communication Architecture: WebSocket & NATS JetStream Bridge

In this architecture, **NATS JetStream** functions as the internal Cloud event bus for asynchronous DAG node execution, while **WebSocket Secure (WSS)** serves as the real-time, persistent transport layer between Toro Cloud and the local Desktop App / `toro-sage-connector`.

This reuses the existing, production-proven WebSocket infrastructure implemented in:
- **WebSocket Connection Manager**: [hub.go](file:///Users/Yankz/programming/usetoro/go/internal/wshandler/hub.go) and [client.go](file:///Users/Yankz/programming/usetoro/go/internal/wshandler/client.go) in package `wshandler`.
- **NATS Consumer Bridge**: [workflow_consumer.go](file:///Users/Yankz/programming/usetoro/go/internal/wshandler/workflow_consumer.go) which consumes NATS events and projects them to client connections.
- **Message Router & Event Types**: [message_handler.go](file:///Users/Yankz/programming/usetoro/go/internal/wshandler/message_handler.go) and [messages.go](file:///Users/Yankz/programming/usetoro/go/internal/wshandler/messages.go).
- **ASE DAG Engine & Topology**: [dag.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/dag.go) executing topology [pcm_bank_reconciliation.yml](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/dags/pcm_bank_reconciliation.yml).
- **ERP Integration & Connector Pattern**: [provider.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/provider.go) (`ERPProvider`), [models.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/models.go), and [manager.go](file:///Users/Yankz/programming/usetoro/go/internal/connectors/manager.go).

```
┌────────────────────────────────────────────────────────────────────────────────────────┐
│                                     TORO CLOUD                                         │
│                                                                                        │
│  ┌───────────────────────┐   NATS Sub / Pub   ┌─────────────────────────────────────┐  │
│  │   NATS JetStream      │ ◄────────────────► │   wshandler.Hub / WorkflowConsumer  │  │
│  │  (ASE Internal Bus)   │                    │ (go/internal/wshandler/hub.go)      │  │
│  └───────────────────────┘                    └──────────────────┬──────────────────┘  │
└──────────────────────────────────────────────────────────────────┼─────────────────────┘
                                                                   │
                                                                   │ Bi-Directional WSS Connection
                                                                   │ (WebSocket / WSS)
                                                                   ▼
┌────────────────────────────────────────────────────────────────────────────────────────┐
│                              LOCAL DESKTOP / SAGE HOST                                 │
│                                                                                        │
│  ┌──────────────────────────────────────────────────────────────────────────────────┐  │
│  │                    toro-sage-connector (Go Daemon / Desktop)                     │  │
│  │                                                                                  │  │
│  │  • WebSocket Client Handler (Listens for task.closed & sage.write.job events)    │  │
│  │  • Local SQL Driver / .PNM Exporter (Writes entries to Sage 100)                 │  │
│  └──────────────────────────────────────────────────────────────────────────────────┘  │
└────────────────────────────────────────────────────────────────────────────────────────┘
```

#### 1. The Role of WebSocket in the System
- **Bi-Directional Event Streaming**: Leverages `wshandler.Client` ([client.go](file:///Users/Yankz/programming/usetoro/go/internal/wshandler/client.go)) to maintain an active connection. Pushes real-time OCR results, Shannon Entropy confidence scores, and Task status transitions (`OPEN` $\rightarrow$ `HOLD_MISSING_DOCS` $\rightarrow$ `CLOSED`) straight to the UI.
- **Firewall & Proxy Compatibility**: Runs over standard WSS (Port 443), ensuring zero friction when connecting through restrictive corporate proxies or firewalls at Moroccan accounting firms.
- **Unified Infrastructure**: Directly extends existing message types in [messages.go](file:///Users/Yankz/programming/usetoro/go/internal/wshandler/messages.go) without introducing new protocol dependencies or compiler overhead.

#### 2. Room-Based Multi-Tenant Isolation in `wshandler.Hub`
In the existing implementation ([hub.go](file:///Users/Yankz/programming/usetoro/go/internal/wshandler/hub.go)), every connected Desktop Client / Connector is registered into a isolated **Room** mapped by its unique `entity_id` (Tenant/Client ID):

- **Room Data Structure**: `Hub.rooms map[string]map[*Client]bool` in [hub.go](file:///Users/Yankz/programming/usetoro/go/internal/wshandler/hub.go#L16).
- **Automatic Registration**: On WebSocket connection, `client.entityID` registers the desktop agent directly into its tenant room.
- **Multi-Tenant Privacy**: Prevents message leakage between different accounting firms (*Fiduciaires*) or client databases.

#### 3. Targeted NATS-to-Room Routing Pattern (`WorkflowEventConsumer`)
The existing [workflow_consumer.go](file:///Users/Yankz/programming/usetoro/go/internal/wshandler/workflow_consumer.go) handles NATS JetStream events and projects them directly to the matching desktop client room:

1. **Cloud Event Generation (NATS JetStream)**: As the Autonomous Semantic Engine ([dag.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/dag.go)) executes nodes in [pcm_bank_reconciliation.yml](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/dags/pcm_bank_reconciliation.yml), domain tools ([pcm_bank_reconciliation_tools.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/domain_tools/pcm/pcm_bank_reconciliation_tools.go)) publish state events to NATS JetStream streams (e.g. `WORKFLOWS`, subject `workflow.events.>`).
2. **Room Extraction & Targeted Projection**: In [workflow_consumer.go](file:///Users/Yankz/programming/usetoro/go/internal/wshandler/workflow_consumer.go#L88-L154):
   - `handleWorkflowStatusEvent` unmarshals the NATS payload and extracts the target `entity_id` (or `realm_id` / `session_id`).
   - It formats a `MessageTypeWorkflowStatus` WebSocket message.
   - It invokes `c.hub.BroadcastToRoom(roomID, wsMsg)`, routing the payload *only* to the connected desktop clients inside that specific room.
3. **WebSocket-to-NATS Ingestion**: When a user drops a missing document onto an open Task in the Desktop App, `message_handler.go` receives the client's WebSocket frame and publishes a `task.resume` event onto NATS JetStream, triggering `DomainTool.ResumeAgent()` in [dag.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/dag.go).

---

## 3. Sage 100 Database Schema & Introspection

### Core Tables Handled

| Sage Table | Entity | Key Fields Synced |
| :--- | :--- | :--- |
| **`F_COMPTEG`** | General Accounts | `CG_Num`, `CG_Intitule`, `N_Nature`, `CG_Classement` |
| **`F_COMPTET`** | Third-Party Auxiliary | `CT_Num`, `CT_Intitule`, `CG_Num`, `CT_Identifiant` (ICE), `CT_Adresse` |
| **`F_ECRITUREC`** | Accounting Entries | `EC_No`, `JO_Num`, `JM_Date`, `CG_Num`, `CT_Num`, `EC_Montant`, `EC_Sens`, `EC_RefPiece`, `EC_Lettrage` |
| **`F_JOURNAUX`** | Accounting Journals | `JO_Num`, `JO_Intitule`, `JO_Type` (Achats, Ventes, Banque) |
| **`F_TAXE`** | TVA Codes | `TA_Code`, `TA_Intitule`, `TA_Taux`, `CG_Num` |

### Dynamic UDF Discovery Query

The Go driver runs runtime metadata queries to discover client-specific custom fields without hardcoding:

```sql
SELECT 
    c.name AS field_name,
    t.name AS data_type,
    c.max_length AS field_length
FROM sys.columns c
JOIN sys.tables tbl ON c.object_id = tbl.object_id
JOIN sys.types t ON c.user_type_id = t.user_type_id
WHERE tbl.name IN ('F_COMPTET', 'F_ECRITUREC', 'F_DOCENTETE')
  AND (c.name LIKE 'CB%' OR c.name LIKE 'TORO_%');
```

---

## 4. Accounting Entry Generation & Moroccan Rules Engine

To ensure Sage accepts imported entries without error or fallback to `471000 (Compte d'Attente)`, the Go Connector enforces strict Moroccan PCM validation rules powered by the project's existing accounting knowledge bases:
- **Moroccan Chart of Accounts Master**: [chart_of_accounts_morocco.json](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/chart_of_accounts_morocco.json)
- **PCM Explanations & Tax Rules**: [plan_comptable_explainations.json](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/plan_comptable_explainations.json)

### Standard 3-Line Purchase Entry (Journal `ACH`)

For a 15,000.00 MAD TTC invoice from vendor *Maroc Telecom* (20% TVA):

```
Line 1 [DEBIT]  : General Account 61440000 (HT Amount)      --> 12,500.00 MAD
Line 2 [DEBIT]  : General Account 34552000 (TVA Récup/Chg)  -->  2,500.00 MAD
Line 3 [CREDIT] : General Account 44110000 + Aux "IAM001"   --> 15,000.00 MAD (TTC)
```

### Mandatory Entry Constraints

1. **Balance Check**: `ABS(SUM(Debit) - SUM(Credit)) < 0.001` per document chain.
2. **Auxiliary Linking**: Every `4411XXXX` entry MUST include the auxiliary vendor code (`CT_Num`).
3. **TVA Date Deductibility**: Cash-basis TVA settlement dates (`Date de Règlement`) are attached to the bank journal entry (`BQ`).

---

## 5. Toro Audit UDF Injection

When writing programmatically to Sage, the connector populates dedicated User Defined Fields on `F_ECRITUREC` to display Toro AI metadata directly inside the accountant's native Sage interface:

* `TORO_MATCH_STATUS`: `"4_WAY_MATCHED"` / `"HOLD_AMBIGUOUS"`
* `TORO_CONFIDENCE`: `"0.99"`
* `TORO_DOC_CHAIN`: `"BC-8841 | BL-1092 | FACT-4412"`
* `TORO_TVA_DATE`: `"2026-07-15"`

---

## 6. Multi-Dossier & Multi-Tenant Support

Accounting firms (*Fiduciaires*) manage dozens of client company files. The connector supports multi-dossier management via configuration:

```yaml
# toro-sage-connector.yml
tenant_id: "fiduciaire-casablanca-01"
toro_cloud_endpoint: "wss://wss.usetoro.com:443/ws"
api_key: "env:TORO_AGENT_API_KEY"

databases:
  - client_id: "client-alpha-sarl"
    sage_db_name: "DOSSIER_ALPHA"
    dsn: "sqlserver://sa:password@localhost:1433?database=DOSSIER_ALPHA"
    sync_interval_seconds: 300

  - client_id: "client-beta-sa"
    sage_db_name: "DOSSIER_BETA"
    dsn: "sqlserver://sa:password@localhost:1433?database=DOSSIER_BETA"
    sync_interval_seconds: 300
```

---

## 7. Implementation Roadmap

### Phase 1: MVP Go Connector (Current Scope)
- [ ] Implement Go SQL reader module (`go/internal/erp/sage/reader/`).
- [ ] Implement Sage Paramétrable `.PNM` exporter (`go/internal/erp/sage/pnm/`).
- [ ] Implement schema introspector for UDF discovery (`go/internal/erp/sage/introspect/`).
- [ ] Add `journal_entry_builder` DAG node to `pcm_bank_reconciliation.yml`.

### Phase 2: Full Native Toro ERP Engine
- [ ] Native PCM General Ledger engine in AlloyDB / Postgres.
- [ ] Automated Liasse Fiscale & DGI XML generator (SIMPL-IS / SIMPL-TVA).
- [ ] Full Sage migration utility (1-click export/import).
