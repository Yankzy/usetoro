# Implement Dynamic Delegation (Option 3)

Introduce a new FIPA Performative (`DELEGATE`) allowing Go-based agents in the system to dynamically decompose tasks into sub-tasks at runtime, while leveraging the Orchestrator's existing `SubWorkflow` execution engine.

## Addressing Agent Discovery, Decision Making, and Safety Limits

### 1. How Agents Decide to Delegate
The Orchestrator handles the *mechanics* of delegation, but the *decision* happens inside the Go-based agents (e.g. within `tap/agents/`). 
- **LLM Function Calling**: If an agent relies on an LLM, the LLM prompt is configured with a persona (e.g., "Manager Agent") and provided a "delegate_task" tool. The LLM determines the task is too complex and triggers the tool. The Go agent receives the tool call and maps it to a `DELEGATE` FIPA envelope.
- **Programmatic Routing**: Non-LLM routing agents can programmatically evaluate incoming data and decide to decompose the task and emit a `DELEGATE` message based on business logic.

### 2. Capability Discovery via Almanac
For a Go agent to dynamically build a sub-workflow, it must know what capabilities exist. 
- Agents will utilize the existing **Almanac** registry via the NATS subject `core.SubjectAlmanacQuery` (`almanac.query`).
- Before sending a `DELEGATE` performative, the delegating Go agent will perform a discovery query to the Almanac. This returns the list of registered capabilities (both `agents.*` and `workers.*`).
- The agent's logic will use these validated `activity_type` strings when constructing the `WorkflowStep` array in its payload.

### 3. Delegation Safety Limits
To prevent a runaway agent from generating massive sub-workflows, the Orchestrator will enforce strict limits on dynamic delegations.
- **Max Steps Limit**: A constant `MaxDynamicDelegationSteps = 50` will be enforced.
- If a `DELEGATE` payload requests more than 50 steps, the Orchestrator will reject the request, automatically failing the step, and placing the parent workflow into a `suspended` state.

---

## Proposed Changes

### tap/pkg/core/verbs.go
Add the new `DELEGATE` performative.
#### [MODIFY] verbs.go
- Add `DELEGATE Performative = "delegate"` to the list of execution verbs.
- Add `DELEGATE` to the `validPerformatives` map.

### tap/workflows/orchestrator.go
Handle the new `DELEGATE` performative by wrapping the agent's provided steps into a dynamically generated, ephemeral `WorkflowDef` and executing it as a SubWorkflow.
#### [MODIFY] orchestrator.go
- Define a new constant `MaxDynamicDelegationSteps = 50`.
- Define a new struct `DelegationRequest` to parse the `DELEGATE` payload:
  ```go
  type DelegationRequest struct {
      Steps   []WorkflowStep  `json:"steps"`
      Payload json.RawMessage `json:"payload,omitempty"`
  }
  ```
- In `handleIncoming`, add a `case core.DELEGATE:` switch statement.
- Within the `DELEGATE` handler:
  1. Parse the `ConversationID` to identify the `instancePath` and parent `stepID`.
  2. Unmarshal the `env.Body` into `DelegationRequest`.
  3. **Safety Check**: If `len(req.Steps) > MaxDynamicDelegationSteps`, immediately fail the step and suspend the workflow instance.
  4. Generate a dynamic workflow name (e.g., `dynamic-<stepID>-<uuid>`).
  5. Create a new `WorkflowDef` object using the name and the steps provided by the agent.
  6. Normalize and marshal the `WorkflowDef`.
  7. Persist the ephemeral blueprint to the database via `o.queries.UpsertWorkflowBlueprint`.
  8. Safely append it to the Orchestrator's in-memory `o.blueprints` slice to allow immediate execution.
  9. Synthesize a `WorkflowStep` mapping to this dynamic blueprint: `WorkflowStep{ID: stepID, SubWorkflow: dynamicName}`.
  10. Call the existing `spawnSubWorkflow` logic using this synthetic step and the provided payload.
- As a result, the parent step remains "Active". When the dynamically generated DAG completes, `completeParentStep` will automatically mark the parent step as "Completed" and forward the final sub-task proof down the original DAG.

### tap/workflows/README.md
Update documentation to reflect the new capabilities.
#### [MODIFY] README.md
- Document the "Dynamic Delegation" pattern.
- Explain the FIPA `DELEGATE` performative and the expected JSON payload.
- Describe how it seamlessly integrates with the existing DAG visibility and SubWorkflow completion mechanics.
- Highlight the requirement for Go agents to use the Almanac for capability discovery and state the `MaxDynamicDelegationSteps` limit.

## Verification Plan

### Manual Verification
- A custom test agent or manual NATS pub event can be used to send a `DELEGATE` envelope with a simple 2-step dynamic DAG.
- Verify that the Orchestrator processes the `DELEGATE` payload, successfully inserts the ephemeral blueprint into the database, and begins dispatching the dynamic steps.
- Verify that sending >50 steps correctly triggers a workflow suspension.
- Ensure the parent workflow instance resumes and completes properly once the dynamic sub-steps issue their `INFORM` performatives.
