# Toro Agent SDK (`tap/pkg/agent`)

Provides the core interface to build and orchestrate AI Agents, Tools, and Workers.

## Definitions

To maintain clear boundaries in the codebase, we differentiate components as follows:

- **Agent**: An actor powered by an LLM loop. It receives JetStream events, evaluates prompt contexts to make decisions, and emits state changes via Redux JSON Patches (e.g., `CSVMappingAgent`).
- **Tool**: A specific function exposed to an LLM via a JSON schema definition. The LLM evaluates its context and decides if it needs to execute the function (e.g., `query_tax_id`).
- **Worker**: A deterministic Go struct that executes specific data pipelines in sequence. It does not use LLM prompt reasoning. It triggers based on specific upstream events (e.g., `EnrichmentWorker` deduplicating rows after insertion).

## Agent Lifecycle Layers

An Agent implementation spans four components: Config, Runtime, Supervisor, and Daemon.

### 1. Config (`defaults.yml`)
Agents require explicit registration in `defaults.yml` to boot.
- Defines identity: Binds the `did:toro:agent` Decentralized Identifier (DID).
- Defines model: Specifies the LLM string (e.g. `gpt-5.1-mini`). The `Runtime` parses this configuration instead of hardcoding target models in code.

### 2. Runtime (`runtime.go`)
Manages the LLM client integration and execution paradigms.
- Uses a `ModelProvider` interface to support dynamic routing and multiple execution paradigms (e.g., standard Chat Completions vs the OpenAI Responses API) based on YAML configuration.
- Exposes `ExecWithMessages()`, which handles structured, native multi-turn message arrays instead of flattened strings.
- Implements an **Autonomous Reasoning Loop**: If the LLM requests tool executions, the runtime safely maps and executes them via the `ToolCallHandler`, then automatically loops back (up to 10 times) to allow the LLM to chain reasoning steps without returning to the main worker loop.
- Bubbles up the entire sequence of intermediate tool calls back to the caller (e.g., `tools.RunAgent`) to ensure perfect, deterministic state memory across Redux circuit-breaker retries.

### 3. Supervisor (`supervisor.go`)
Manages the active Goroutines for running Agents.
- Exposes `RegisterInternalAgent()` to map initialized handler routines.
- Wraps the Agent's `EventBus` to inject global cost mechanisms like `micrion.NewTolledEventBus`.
- Controls application-level `Start()` and `Stop()` runtime loops.

### 4. Daemon (`daemon.go`)
Initializes the Toro host node ecosystem.
- Establishes connections to Postgres and JetStream.
- Iterates over `defaults.yml` configurations and deploys the Supervisor for each active Agent.
- Publishes each Agent's DID Inbox address over NATS to the global `Almanac` ledger, alerting other network instances that the Agent is online.

## Observability Data Flow

To display progress updates to users on the frontend, state changes stream over WebSockets:

1. **State Mutation**: Agents update local database state and generate an `RFC 6902` JSON Patch array using `redux.Store.Reduce()`.
2. **JetStream Pipeline**: The agent publishes the JSON Patch payload to `workflow.trace.{session_id}`.
3. **Web Gateway Proxy**: A core backend server maintains a WebSocket connection with the active client browser. It subscribes to `workflow.trace.{session_id}` and proxies the raw JSON bytes.
4. **React Hydration**: The frontend client receives the payload, merges the JSON patch into its local Redux store component, and triggers a UI update.

## Context Paging, Multi-Tenancy & OKF Integration

To prevent LLM token-bloat and recursive hallucination loops, the SDK implements a two-front paging and obfuscation system:

### 1. BPE UUID Obfuscation (Layer A)
To prevent the LLM from hallucinating or miscalculating standard UUIDs due to Byte-Pair Encoding (BPE) tokenization, the `UUIDMapper` ([mapper.go](file:///Users/Yankz/programming/usetoro/tap/pkg/agent/mapper.go)) intercepts raw input prompts and dynamically obfuscates all UUIDs to simple tags (`ref_1`, `ref_2`). Upon receiving the LLM's response, these refs are reconstructed back into the original UUIDs on the way in.

### 2. Dynamic Context Paging (Layer B)
When evaluating large datasets, the `Runtime` embeds a "Virtual Memory" interceptor wrapped within `runtime.ExecWithPaging()`.
- **Ephemeral Pointer Map**: `GenerateLocalContextMap()` maps the available documents to simple integers (`local_ref: 1, 2`), isolating the LLM from processing raw identifiers.
- **OpenAI Tool Interception**: The `PAGE_IN` tool is exposed to the LLM. When the AI generates `{"tool_call": "PAGE_IN", "local_ref": 1}`, the Go Kernel intercepts execution, translates the `local_ref` to the target path/UUID, and fetches the data using the registered `DocumentFetcher`.
- **Circuit Breaker**: A hard limit (`MaxPages=3`) trips if the AI enters infinite fetching recursion to prevent loop costs.
- **Organic Garbage Collection**: Large message slices and pointer maps reside strictly within the execution block, falling out of scope once the agent completes its run, allowing the Go Garbage Collector to drop the memory footprint.

### 3. Open Knowledge Format (OKF) & Multi-Tenancy
To allow tenants to manage playbooks, rules, and reference guides via their own GitHub repositories, the system integrates the **Open Knowledge Format (OKF)**:
- **Tenant Isolation**: Static OKF files are partitioned on disk by tenant UUID under `docs/knowledge/<realm_id>/`.
- **Tenant-Aware Fetcher**: The [OKFDocumentFetcher](file:///Users/Yankz/programming/usetoro/tap/pkg/agent/paging.go#L80-L109) extracts the tenant's `realm_id` from the context (via `WithRealmID`), cleans and validates paths to protect against directory traversal, and reads the markdown file—automatically stripping the YAML frontmatter before returning the body text.