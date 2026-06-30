# LLM Usage & Token Tracking Audit

## 1. LLM Client Usage Audit

The goal of this audit is to determine if all LLM invocations in the project are routing through the centralized `runtime.go` module (`tap/pkg/agent/runtime.go`). 

### ✅ Centralized Path (The standard `runtime.go` approach)
- **Location:** `tap/pkg/agent/runtime.go` (and associated files in `tap/pkg/agent/` and `tap/pkg/llm/`).
- **Usage:** This is the core Agentic runtime, effectively wrapping `github.com/openai/openai-go/v3` for chat, responses, tool-calling, and paging functionality. 

### ⚠️ Deviations (Clients NOT using `runtime.go`)

Through our audit, we identified several instances where OpenAI clients are being instantiated directly, bypassing the `runtime.go` abstraction:

1. **`go/internal/services/ai/llm_client.go`**
   - **Client:** `openai.NewClient(option.WithAPIKey(apiKey))` (`github.com/openai/openai-go/v3`)
   - **Usage:** Contains a custom wrapper (`LLMClient`) used for specific functionalities:
     - `GenerateJSON`: Uses the Responses API with `ResponseFormatTextConfigUnionParam` to force strict JSON structured outputs into provided struct pointers.
     - `GenerateText`: A helper for simple, non-JSON text completions.
     - `GenerateEmbedding`: Uses `client.Embeddings.New` to generate vector embeddings.
   - **Recommendation:** If the goal is strict convergence, the `GenerateJSON` and `GenerateText` paradigms could be moved into `runtime.go` as specialized execution methods, or at least share the same underlying client factory.

2. **`go/internal/infra/vector/embedder.go`**
   - **Client:** `openai.NewClient(option.WithAPIKey(apiKey))` (`github.com/openai/openai-go/v3`)
   - **Usage:** Defines an `Embedder` specifically for text embeddings (`Embed`, `EmbedBatch`). 
   - **Note:** Since `runtime.go` focuses heavily on completions/responses (agentic reasoning), having a separate embedder might be architecturally acceptable, but it currently does not share the same client initialization logic.

3. **`tap/agents/weather_agent.go`**
   - **Client:** `openai.NewClient()` (`github.com/openai/openai-go/v3`)
   - **Usage:** Directly instantiates the client and makes its own `Responses.New` calls with hardcoded tool schemas. 
   - **Note:** This appears to be a mock/demo agent. It should either be refactored to use the `Runtime` struct or removed if no longer necessary.

4. **Python Multi-Agent Backend (Real-time AI Streaming)**
   - **Context:** While we did not find localized `.py` files importing OpenAI in this specific search, the project knowledge base (KI: *Real-time AI Conversational Streaming Backend*) dictates a specialized Python multi-agent backend heavily relying on the **OpenAI Realtime API**.
   - **Note:** This is inherently separate from the Go `runtime.go` flow and will require a similar tracking approach natively in Python.

---

## 2. Token Tracking Strategy

Currently, there is no explicit token tracking or cost calculation present in the `runtime.go` or `llm_client.go` execution flows.

### Goal
Track token costs when the LLM returns and match them to clients, completely decoupled, without polluting the LLM request layer with user or client context.

### Proposed Strategy

**1. Extraction Spot: The Return Boundary**
The most appropriate place to extract token usage is immediately after the OpenAI client returns the response, inside `runtime.go` (e.g., in `Exec`, `execWithMessagesResponses`, `execWithMessagesChat`) and `llm_client.go` (if kept).

The OpenAI response objects (e.g., `resp` returned by `client.Responses.New` or `client.Chat.Completions.New`) contain a `Usage` struct:
```go
// Example extraction
usage := resp.Usage
promptTokens := usage.PromptTokens
completionTokens := usage.CompletionTokens
totalTokens := usage.TotalTokens
modelUsed := resp.Model // or the effective model requested
```

**2. Asynchronous Publishing (NATS)**
Upon extracting the usage, immediately publish a lightweight event to NATS (e.g., subject `llm.usage.tracked`).
- **Payload:** Include the `Model`, `PromptTokens`, `CompletionTokens`, `TotalTokens`, a timestamp, and a `CorrelationID`.
- **CorrelationID:** This is critical. The LLM function shouldn't know who the "user" is, but it does have context of the task (e.g., via the NATS message `msg.Reply` subject or a UUID injected into `context.Context` when the task was triggered). 

**3. Decoupled Client Matching (Aggregator Service)**
To link this cost back to the specific client without coupling:
- The system that originally requests the agent's help (e.g., the GraphQL resolver, an API handler, or a Background Worker) knows the `ClientID` and generates the `CorrelationID` (or Task ID). 
- When it requests the LLM work, it publishes its own event: `task.initiated { CorrelationID, ClientID }`.
- A separate, standalone **Billing/Token Aggregator** service consumes from NATS. It listens for both `task.initiated` and `llm.usage.tracked`.
- The aggregator joins the two events in the database using the `CorrelationID`. It calculates the financial cost based on the `Model` and `TotalTokens`, and updates the specific client's usage ledger.

This completely separates the LLM reasoning layer from billing/client logic while providing accurate, real-time cost tracking.
