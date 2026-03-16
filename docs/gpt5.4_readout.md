# GPT-5.4 Capabilities & Migration Notes

Based on the [OpenAI documentation](https://developers.openai.com/api/docs/guides/latest-model), here are the key features and migration requirements for the new `gpt-5.4` model:

## 1. Key Improvements
- **Coding & Agentic Workflows**: Brings the coding capabilities of `GPT-5.3-Codex` to the flagship model. It is better at multi-file changes, long-running agent trajectories, and end-to-end performance on tool-heavy workloads.
- **1M Token Context**: Supports a massive 1M token context window, allowing for analysis of entire codebases or long document collections in a single request.
- **Native Compaction**: Trained to support compaction, meaning it can handle longer agent trajectories while preserving key context efficiently.

## 2. Powerful New Tool Capabilities
- **Built-in Computer Use**: GPT-5.4 is the first mainline model that can directly operate software through a user interface by inspecting screenshots and navigating a site or filling out forms.
- **Tool Search (Deferred Loading)**: For large ecosystems (many functions/MCPs), you can use `tool_search`. The model only loads the tool definitions it actually needs at runtime, drastically saving tokens and improving latency.
- **Custom Tools (Context-Free Grammars)**: You can define `type: custom` to let the model send freeform text (like raw SQL, shell commands, or Python code) instead of JSON. Even better, you can constrain these outputs using a **Lark grammar (CFG)** to force the model to adhere to a strict DSL or syntax!

## 3. API Changes & Parameter Compatibility
- **New `phase` Parameter**: For long-running flows in the Responses API, there is a new `phase` field on assistant messages. It should be used to distinguish intermediate reasoning (`phase: "commentary"`) from the final output (`phase: "final_answer"`).
- **Strict Parameter Restraints**:
  - The parameters `temperature`, `top_p`, and `logprobs` are **ONLY supported if `reasoning: { effort: "none" }` is set**.
  - If you want reasoning (*which is on by default*), you must NOT send `temperature`. Instead, you use `reasoning: { effort: ... }` and `text: { verbosity: "low" | "medium" | "high" }`.

*(Note: In our previous Go code update in `llm_client.go`, we explicitly set `Temperature: 0.1`. If we switch `model` to `gpt-5.4`, that request will **fail** unless we also specify `reasoning: { effort: "none" }`!)*
