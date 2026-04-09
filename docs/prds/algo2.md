# Technical PRD: Algorithm 3 - LLM Memory Paging Architecture & Local Context Mapping

### 1. Objective
To solve context window exhaustion, historical data hallucination, and API cost inflation in long-running AI workflows. Algorithm 3 implements a "Virtual Memory" Operating System pattern. The Go Kernel provides the LLM with a lightweight, hallucination-proof index of available historical data. The LLM explicitly requests full-text documents into its working memory only when needed, protected by strict architectural circuit breakers.
# Technical PRD: Algorithm 3 - LLM Memory Paging & Local Context Architecture

### 1. Executive Summary
The objective of Algorithm 3 is to solve context window exhaustion, historical data hallucination, and API cost inflation in long-running AI workflows. It implements a "Virtual Memory" Operating System pattern. Instead of appending entire document histories into the LLM prompt, the Go Kernel provides the LLM with a lightweight, hallucination-proof index of available data. The LLM acts as a CPU that explicitly requests full-text documents into its working memory (`PAGE_IN`) only when needed for reasoning, protected by strict architectural circuit breakers and deterministic pointer translation.

### 2. Core Infrastructure Components
* **The Orchestrator / Tool Executor:** Go Kernel (Intercepts memory tool calls, translates safe aliases to database UUIDs, fetches data, and manages context garbage collection).
* **The Paging Directory:** A dynamic, Go-managed metadata map tracking documents, emails, and past reasoning chains.
* **The Data Store:** Postgres / AWS S3 (Holds the actual uncompressed raw text, JSONs, and PDF extractions).
* **The Vector Store:** Pinecone (Handles semantic search for older, archived memory blocks to prevent index bloat).
* **The CPU:** LLM (Evaluates state and executes explicit tool calls to fetch required memory).



### 3. Engineered Solutions to Known Vulnerabilities (Baked-In Patches)

**A. Vulnerability: UUID Hallucination & BPE Token Bloat**
* **The Fix: Local Context Pointer Map.** LLMs struggle to accurately output high-entropy strings like UUIDs (`f47ac10b...`) due to Byte-Pair Encoding (BPE), leading to failed tool calls. The Go Kernel completely abstracts UUIDs from the LLM. It generates an ephemeral, in-memory map pairing low-entropy integers (e.g., `1`, `2`) to real database UUIDs. The LLM only sees and outputs the integer alias (`local_ref: 1`). The Go Kernel intercepts this integer and translates it back to the UUID before querying the database.

**B. Vulnerability: The Infinite "Page-In" Loop**
* **The Fix: The OS Circuit Breaker.** To prevent an LLM from getting confused and endlessly paging documents in and out (burning API credits), the Go Kernel enforces a strict `MAX_PAGES_PER_CYCLE` limit (e.g., 3). If the LLM attempts a 4th `PAGE_IN` request during a single wake-up cycle, the Kernel blocks the tool call, returns a system error to the prompt, and forces the LLM to either emit a state-change event or escalate to `HUMAN_FALLBACK`.

**C. Vulnerability: Context Window Index Bloat**
* **The Fix: Hierarchical Paging (Semantic Search Integration).** Injecting a "Table of Contents" of 500 historical documents into the prompt reconstructs the exact token-bloat problem it aims to solve. The Go Kernel caps the injected "Paging Directory" at the 5 most recent or highly relevant documents. For everything else, the LLM is provided a secondary `SEARCH_INDEX("query")` tool to semantically query the Vector DB, which returns the alias for the older document to then be paged in.

### 4. Data Models & Schemas

**A. The Local Context Map (Go In-Memory / Ephemeral)**
Created by the Go Kernel right before prompting the LLM. Exists only for the duration of the wake-up cycle.
```go
localContextMap := map[int]string{
    1: "f47ac10b-58cc-4372-a567-0e02b2c3d479", // Broker Rate Confirmation
    2: "a12bc99e-11aa-44bb-b123-999988887777", // Bill of Lading
}
```

**B. The Paging Directory (Injected into the LLM Prompt)**
The LLM sees a highly compressed, low-entropy list of available memory blocks in its system prompt.
```json
"available_pages": [
  {
    "local_ref": 1,
    "type": "BROKER_RATE_CONFIRMATION",
    "summary": "Original signed rate confirmation detailing linehaul and lumper fees."
  },
  {
    "local_ref": 2,
    "type": "BILL_OF_LADING",
    "summary": "Proof of delivery signed at receiver dock."
  }
]
```

**C. The Tool Schemas (OpenAI Function Calling)**
The Go Kernel binds these schemas to the LLM, restricting it to use only hallucination-proof integers or targeted search queries.
```json
[
  {
    "name": "PAGE_IN",
    "description": "Fetch the full raw text of a document using its local reference number.",
    "parameters": {
      "type": "object",
      "properties": {
        "local_ref": {
          "type": "integer",
          "description": "The exact integer local_ref from the available_pages directory."
        }
      },
      "required": ["local_ref"]
    }
  },
  {
    "name": "SEARCH_INDEX",
    "description": "Search the deep archives for older documents not listed in available_pages.",
    "parameters": {
      "type": "object",
      "properties": {
        "query": {
          "type": "string",
          "description": "Semantic search query (e.g., 'fuel surcharge policy from March')."
        }
      },
      "required": ["query"]
    }
  }
]
```

### 5. The Complete Execution Flow (The Paging Loop)

**Step 1: Context & Map Construction**
* The Go Kernel wakes up (triggered by Algorithm 1).
* It fetches the latest 5 documents related to the workflow from Postgres.
* It generates the `localContextMap` (mapping integers 1-5 to their respective UUIDs).
* It injects the lightweight `available_pages` JSON array into the LLM prompt alongside the Current State Object.

**Step 2: The LLM Evaluation**
* The LLM determines it cannot process the current trigger without reading the exact terms of the Broker Rate Confirmation. 
* It safely outputs the integer alias without risk of BPE hallucination:
  `{"tool_call": "PAGE_IN", "arguments": {"local_ref": 1}}`

**Step 3: The Go Kernel Intercept & Translation**
* The Go Kernel intercepts the tool call.
* *Circuit Breaker Check:* The Kernel increments a temporary `page_count` variable. If `page_count > 3`, it aborts the fetch and returns an error message to the LLM.
* *Translation:* The Kernel looks up `1` in the `localContextMap` and retrieves UUID `f47ac10b-58cc-4372-a567-0e02b2c3d479`.
* *Data Retrieval:* The Kernel queries Postgres/S3 using the real UUID and retrieves the heavy, raw text of the contract.

**Step 4: Page Injection & Re-invocation**
* The Go Kernel formats the raw text into a standard `tool_response` message.
* It appends this response to the active context array and calls the LLM API again.
* The LLM now has perfect recall of the uncompressed document within its working memory ("RAM") and can proceed to execute its logic.

**Step 5: Context Garbage Collection (Page Eviction)**
* Once the LLM successfully emits its final state-change event (handing control back to Algorithm 1 to update NATS), the wake-up cycle ends.
* The Go Kernel deletes the ephemeral `localContextMap` from memory.
* The Go Kernel discards the bulky `tool_response` message containing the raw document.
* The agent is suspended. When it wakes up next, its memory footprint has dropped back down to just the base State Object and the lightweight index.
### 2. Core Infrastructure Components
* **The Paging Directory:** A dynamic, Go-managed metadata map tracking documents, emails, and past reasoning chains.
* **The Vector Store (Pinecone):** Handles semantic search for older, archived memory blocks to prevent index bloat.
* **The Data Store (Postgres/S3):** Holds the actual uncompressed raw text and PDF data.
* **The Tool Executor (Go Kernel):** Intercepts memory tool calls, translates safe aliases to database UUIDs, fetches data, and manages context garbage collection.

### 3. Known Vulnerabilities & Engineered Solutions

**A. Vulnerability: UUID Hallucination & Token Bloat**
* **The Problem:** LLMs use Byte-Pair Encoding (BPE), meaning high-entropy strings like UUIDs (`f47ac10b...`) are shattered into multiple tokens. If an LLM is forced to output a UUID to call a tool, it frequently hallucinates characters, causing database queries to fail. Furthermore, loading 50 UUIDs into a prompt wastes input tokens.
* **The Solution: Local Context Pointer Map.** The Go Kernel completely abstracts UUIDs from the LLM. It generates an ephemeral, in-memory map pairing low-entropy integers (e.g., `1`, `2`) to real database UUIDs. The LLM only sees and outputs the integer alias (`local_ref: 1`). The Go Kernel intercepts the integer and translates it back to the UUID before querying Postgres.

**B. Vulnerability: The Infinite "Page-In" Loop**
* **The Problem:** An LLM might get confused by a document, page in another, get confused again, and page in the first one repeatedly, burning through API credits without ever emitting a state-change event.
* **The Solution: The OS Circuit Breaker.** The Go Kernel enforces a strict `MAX_PAGES_PER_CYCLE` limit (e.g., 3). If the LLM attempts a 4th `PAGE_IN` request during a single wake-up, the Kernel blocks the tool call, returns a system error, and forces the LLM to either emit a state-change event or escalate to a human operator.

**C. Vulnerability: Index Context Bloat**
* **The Problem:** Injecting a "Table of Contents" of 500 historical documents into the prompt reconstructs the exact context-window problem we are trying to avoid.
* **The Solution: Hierarchical Paging.** The Go Kernel only injects the 5 most recent or highly relevant document aliases into the prompt's Index. For everything else, the LLM is provided a `SEARCH_INDEX("query")` tool to semantically query Pinecone, which returns the alias for the older document.

### 4. Data Models & Schemas

**A. The Local Context Map (Go In-Memory / Ephemeral)**
Created by the Go Kernel right before prompting the LLM.
```go
localContextMap := map[int]string{
    1: "f47ac10b-58cc-4372-a567-0e02b2c3d479", // Broker Rate Confirmation
    2: "a12bc99e-11aa-44bb-b123-999988887777", // Bill of Lading
}
```

**B. The Paging Directory (Injected into the LLM Prompt)**
The LLM sees a highly compressed, low-entropy list of available memory blocks.
```json
"available_pages": [
  {
    "local_ref": 1,
    "type": "BROKER_RATE_CONFIRMATION",
    "summary": "Original signed rate confirmation detailing linehaul."
  },
  {
    "local_ref": 2,
    "type": "BILL_OF_LADING",
    "summary": "Proof of delivery signed at receiver dock."
  }
]
```

**C. The Tool Schema (OpenAI Function Calling)**
The Go Kernel binds this schema to the LLM, restricting it to use only the hallucination-proof integer.
```json
{
  "name": "PAGE_IN",
  "description": "Fetch the full raw text of a document using its local reference number.",
  "parameters": {
    "type": "object",
    "properties": {
      "local_ref": {
        "type": "integer",
        "description": "The exact integer local_ref from the available_pages directory."
      }
    },
    "required": ["local_ref"]
  }
}
```

### 5. The Execution Flow (The Paging Loop)

**Step 1: Context & Map Construction**
The Go Kernel wakes up. It fetches the latest 5 documents related to the workflow, generates the `localContextMap`, and injects the integer-based `available_pages` array into the LLM prompt.

**Step 2: The LLM Evaluation**
The LLM determines it needs to read the Broker Rate Confirmation. It safely outputs the integer alias without risk of BPE hallucination:
`{"tool_call": "PAGE_IN", "arguments": {"local_ref": 1}}`

**Step 3: The Go Kernel Intercept & Translation**
1. The Go Kernel intercepts the tool call.
2. *Circuit Breaker Check:* The Kernel increments the `page_count` variable. If `page_count > 3`, it aborts and returns an error to the LLM.
3. *Translation:* The Kernel looks up `1` in the `localContextMap` and retrieves UUID `f47ac10b...`.
4. *Data Retrieval:* The Kernel queries Postgres/S3 using the real UUID and retrieves the raw text.

**Step 4: Page Injection & Re-invocation**
The Go Kernel formats the raw text into a `tool_response` message, appends it to the active context array, and calls the LLM API again. The LLM now has perfect recall of the document and processes the workflow logic.

**Step 5: Context Garbage Collection (Page Eviction)**
Once the LLM successfully emits its final state-change event (Algorithm 1), the Go Kernel deletes the ephemeral `localContextMap` and discards the bulky `tool_response` message containing the raw document. The LLM is suspended, and the memory footprint drops back to zero.