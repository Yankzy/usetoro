# Context Paging API (TAP Agent SDK)

The Context Paging API limits the context window bloat caused by appending large datasets (PDFs, lengthy JSONs, large database objects) to the OpenAI `messages` array. Instead of injecting full-text strings, it generates an ephemeral integer-to-UUID dictionary (`localContextMap`), restricting the LLM to query specific chunks of data explicitly via the `PAGE_IN` Tool Call.

## 1. Paging Types & Generation (`tap/pkg/agent/paging.go`)

Agents pass an array of `PageContext` metadata structs to the Runtime before execution. 

```go
// PageContext represents a single document indexed in the context pager map.
type PageContext struct {
	Type    string `json:"type"`
	Summary string `json:"summary"`
	UUID    string `json:"-"` // Hidden from LLM serialization natively
}
```

Prior to execution, `GenerateLocalContextMap` returns a dictionary mapping `1-indexed` integers (`local_ref`) to the raw `UUID`s, isolating the LLM from processing high-entropy Byte-Pair Encoding (BPE) structures:

```go
localMap, pagesJSON := GenerateLocalContextMap(pages)
// localMap: map[1:"f47ac1...", 2:"a12bc9..."]
// pagesJSON: [{"local_ref":1, "type":"RECEIPT", "summary":"Gas Station Receipt"}]
```

## 2. Global Execution (`tap/pkg/agent/runtime.go`)

The core execution wrapper replaces standard prompt calls. It accepts the context array and a data-fetching closure (`DocumentFetcher`).

```go
// DocumentFetcher defines the external database callback resolving UUID to raw text
type DocumentFetcher func(ctx context.Context, uuid string) (string, error)

func (r *Runtime) ExecWithPaging(
    ctx context.Context, 
    prompt string, 
    pages []PageContext, 
    fetcher DocumentFetcher,
) (string, error)
```

### Prompt Injection
`ExecWithPaging` appends the strictly sanitized `pagesJSON` directly to the `SystemPrompt`, forcing the LLM to review the available "Table of Contents".

## 3. Tool Calling & Circuit Breakers

The runtime provisions a unified `PAGE_IN` function bound to `gpt-4o-mini`. 

```json
{
  "name": "PAGE_IN",
  "description": "Fetch the full raw text of a document using its local reference number.",
  "parameters": {
    "type": "object",
    "properties": {
      "local_ref": { "type": "integer" }
    },
    "required": ["local_ref"]
  }
}
```

### The Paging Loop
1. **Tool Invocation**: The LLM outputs `{"tool_call": "PAGE_IN", "local_ref": 1}`.
2. **Circuit Breaker Check**: The runtime tracks iterations via an implicit integer (`apiCalls`). If `apiCalls > 3` (Max Pages), it aborts execution and forcefully triggers an error block to prevent infinite loops.
3. **Array Translation**: The runtime retrieves the `local_ref` integer, looks it up in `localContextMap` to get the `UUID`, and executes the `DocumentFetcher` closure.
4. **Injection**: The raw blob is appended dynamically to the `messages` array as a standard tool response.

## 4. Garbage Collection Mechanics
Because `localMap`, the `messages` array, and the raw text buffers are formally scoped entirely inside the `ExecWithPaging` function execution block, standard Go Garbage Collection implicitly frees memory directly after the loop concludes and the Redux state is returned.
