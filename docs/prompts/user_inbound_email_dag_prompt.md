TAP User Inbound Email DAG Generation Prompt

Copy the block below and fill in the `[PLACEHOLDERS]` before sending it to an LLM.

---

````
You are an expert system configurator for the Toro Autonomous Semantic Engine (ASE). Generate a YAML file representing the `user_inbound_email` DAG for a newly onboarded user.

### Context
- Tenant ID: `[TENANT_ID]`
- Specific Custom Rules / Tone: `[SPECIFIC_RULES]`

### Framework Contracts You MUST Follow

1. The output must be valid YAML matching the ASE DAG schema, containing `hyper_parameters`, `prompts`, and `dag` sections.
2. The `dag` section must have an `entry_node` named `extract_intent`.
3. The DAG must include at least these three base nodes:
   - `extract_intent` (kind: `classifier`): Analyzes the incoming **email** to extract the user's intent.
   - `resolve_context` (kind: `classifier`): Resolves the intent against active holding states.
   - `route_and_trigger` (kind: `action`): Has `execution_parameters.action_type = "resume_bookkeeping_dag"`.

4. The `prompts` section must contain the specific instructions for each classifier.
   - For the `intent_specialist` prompt, you MUST explicitly mention that the message is an **email** and may contain email signatures, forwards, or attachments.
   - Inject the specific custom rules/tone (`[SPECIFIC_RULES]`) into the prompt so the LLM respects the tenant's preferences when evaluating the email.

### Reference YAML Structure

```yaml
hyper_parameters:
  confidence_threshold: 0.95
  auto_advance: true
  max_llm_retries: 3
  llm_timeout_seconds: 60
  batch_flush_seconds: 1

prompts:
  intent_specialist:
    - |
      You are an Inbound Email Triage node for Tenant: [TENANT_ID]. 
      Read the incoming email and determine the user's intent. Ignore standard email signatures.
      
      Custom Rules: [SPECIFIC_RULES]
      
      Select from the following intents:
      1. PROVIDE_RECEIPT: The user has attached or pasted a receipt/invoice.
      2. CLARIFICATION: The user is answering a question about a transaction.
      3. APPROVAL: The user is explicitly approving a transaction.
      4. UNKNOWN: The intent is not clear or does not match the above.

    - |
        EXPECTED OUTPUT FORMAT:
        [
          {
            "op": "add",
            "path": "/rows",
            "value": {
              "row_id_1": {
              "property": "intent",
              "candidates": {
              "1": { "value": "PROVIDE_RECEIPT", "confidence": 0.99, "reasoning": "User attached a receipt." },
              "2": { "value": "UNKNOWN", "confidence": 0.01, "reasoning": "Fallback." }
                }
              }
            }
          }
        ]

  context_specialist:
    # ... Context resolution prompt ...

dag:
  entry_node: "extract_intent"
  nodes:
    extract_intent:
      kind: "classifier"
      name: "extract_intent"
      prompt_key: "intent_specialist"
      children:
        "PROVIDE_RECEIPT": "resolve_context"
        "CLARIFICATION": "resolve_context"
        "APPROVAL": "resolve_context"
      default_child: "triage_failed"
      
    resolve_context:
      kind: "classifier"
      name: "resolve_context"
      prompt_key: "context_specialist"
      children:
        "CONTEXT_FOUND": "route_and_trigger"
      default_child: "triage_failed"
      
    route_and_trigger:
      kind: "action"
      name: "route_and_trigger"
      execution_parameters:
        action_type: "resume_bookkeeping_dag"
      default_child: "triage_complete"

    triage_failed:
      kind: "terminal"
      name: "triage_failed"

    triage_complete:
      kind: "terminal"
      name: "triage_complete"
```

What to Return

Return only the full, valid YAML configuration for this tenant. Do not wrap it in explanation text.
````
