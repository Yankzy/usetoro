# OpenAI API Usage Analysis

This document identifies all locations in the repository where OpenAI APIs are invoked, categorized by the API type and library used.

## 1. OpenAI-Go SDK (v3) Usage
The codebase uses the modern `openai-go/v3` SDK for core reasoning and embeddings.

### Response API (`client.Responses.New`)
Used for the newer "Response" pattern which often handles multi-turn or tool-enabled logic in a simplified way.
- **[runtime.go](file:///Users/Yankz/programming/usetoro/tap/pkg/agent/runtime.go#L70)**: Used in the `Exec` method for standard agent inference.
- **[llm_client.go](file:///Users/Yankz/programming/usetoro/go/internal/services/ai/llm_client.go#L59)**: A shared wrapper service in the internal Go backend for standardized responses.
- **[weather_agent.go](file:///Users/Yankz/programming/usetoro/tap/agents/weather_agent.go#L46)**: A demonstration agent showing tool-calling and follow-ups via the Response API.

### Chat Completion API (`client.Chat.Completions.New`)
Used for structured tool calling and paging logic.
- **[runtime.go](file:///Users/Yankz/programming/usetoro/tap/pkg/agent/runtime.go#L169)**: Used in `ExecWithPaging` to handle localized document fetching through a tool-calling interceptor.

### Embeddings API (`client.Embeddings.New`)
Used for vectorizing text for search and RAG.
- **[embedder.go](file:///Users/Yankz/programming/usetoro/go/internal/infra/vector/embedder.go#L51)**: Implementation of the `Embedder` for both single string and batch processing.

---

## 2. Legacy / Adapter Layer (`sashabaranov/go-openai`)
The repository contains a multi-provider LLM abstraction layer in `tap/pkg/llm` that utilizes the older `go-openai` types as a common interface.

- **[interfaces.go](file:///Users/Yankz/programming/usetoro/tap/pkg/llm/interfaces.go)**: Defines the `LLM` interface using `openai.ChatCompletionRequest`.
- **[factory.go](file:///Users/Yankz/programming/usetoro/tap/pkg/llm/factory.go)**: Returns an `openai.Client`.
- **[google.go](file:///Users/Yankz/programming/usetoro/tap/pkg/llm/google.go)** & **[anthropic.go](file:///Users/Yankz/programming/usetoro/tap/pkg/llm/anthropic.go)**: Use OpenAI's struct types to return data from non-OpenAI models, acting as a normalization layer.

---

## 3. Configuration & Initialization
Places where the API is initialized or configured via environment variables.

- **[server.go](file:///Users/Yankz/programming/usetoro/go/cmd/graphql/server.go#L152)**: GraphQL entry point checking for `OPENAI_API_KEY`.
- **[main.go](file:///Users/Yankz/programming/usetoro/go/cmd/fignode/main.go#L163)**: Fignode entry point checking for the API key.
- **[daemon.go](file:///Users/Yankz/programming/usetoro/tap/pkg/daemon/daemon.go#L119)**: Protocol Daemon initializing the embedder with the API key.
- **[main.go](file:///Users/Yankz/programming/usetoro/go/cmd/sync/main.go#L140)**: Sync service passing embedding model and dimensions configuration.

---

## 4. Python Worker
- **[requirements.txt](file:///Users/Yankz/programming/usetoro/python-worker/requirements.txt)**: Lists `openai` as a dependency, although no active `.py` code currently calls it directly.
