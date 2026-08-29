# PRD: Agent Execution Stack Primitive

## 1. Purpose

The Agent Execution Stack is a runtime primitive for representing what an agent is currently doing, what subproblem it is currently inside, how that subproblem relates to the work that came before it, and what should happen when that subproblem finishes.

The design borrows the execution model of a programming language call stack, but it adapts it for long-running, asynchronous agent work.

The important idea is not simply that we will store conversations in Redis.

The important idea is that the runtime will represent agent work as **nested execution frames**.

If a user asks:

> What was our revenue last month?

the agent may create one execution frame representing that question.

If, while answering it, the agent needs to determine whether several invoices were actually paid, it can create another frame underneath the first one.

If resolving one invoice requires investigating a bank transaction, another child frame can be created.

The runtime therefore maintains something structurally similar to:

```text
Answer user's revenue question
    Calculate recognized revenue
        Verify invoice INV-182
            Resolve payment TX-901
```

At any moment, the agent should be reasoning primarily inside the lowest active frame.

When that work is finished, the frame returns a result to its parent and is removed from the active execution path.

That operation is analogous to a function returning in a programming language.

The purpose of this primitive is to make the runtime, rather than the LLM, responsible for maintaining this execution hierarchy.

---

# 2. What a Call Stack Actually Is

Before defining the agent primitive, it is useful to be precise about the programming concept we are borrowing.

Consider the following Go program:

```go
package main

import "fmt"

func main() {
	result := calculateRevenue("2026-07")
	fmt.Println(result)
}

func calculateRevenue(period string) int64 {
	invoices := loadInvoices(period)
	return sumInvoices(invoices)
}

func loadInvoices(period string) []int64 {
	return []int64{12000, 18000, 8000}
}

func sumInvoices(values []int64) int64 {
	var total int64
	for _, value := range values {
		total += value
	}
	return total
}
```

When `main()` calls:

```go
calculateRevenue("2026-07")
```

the program must remember several things.

It must remember that `main()` has paused.

It must remember where execution should continue when `calculateRevenue()` returns.

It must also maintain the local state of the new function call.

Conceptually, the runtime creates a **stack frame** for that function invocation.

The stack may now look like:

```text
TOP

calculateRevenue
    period = "2026-07"

main
    waiting for calculateRevenue result

BOTTOM
```

Then `calculateRevenue()` calls:

```go
loadInvoices(period)
```

Another frame is pushed:

```text
TOP

loadInvoices
    period = "2026-07"

calculateRevenue
    period = "2026-07"
    waiting for invoices

main
    waiting for revenue

BOTTOM
```

When `loadInvoices()` returns, its frame disappears from the active call stack.

Its return value is handed back to `calculateRevenue()`.

The active stack becomes:

```text
TOP

calculateRevenue
    period = "2026-07"
    invoices = [...]

main
    waiting for revenue

BOTTOM
```

This removal is called **popping the stack**.

When `calculateRevenue()` later returns, its own frame is popped.

Execution resumes in `main()`.

Therefore:

**PUSH** means entering another function invocation.

**POP** means the invocation has returned and its temporary execution frame is removed.

The important thing about a conventional call stack is not that it happens to use a stack data structure.

The important property is:

> Every piece of work knows who called it, what local state belongs to it, and where its result should return.

That is the property Toro should reproduce.

---

# 3. Why the Agent Runtime Needs the Same Primitive

An LLM conversation does not naturally have this structure.

Suppose an accountant sends:

> What was our revenue last month?

The agent answers:

> 428,000 MAD.

Then:

> How much of that is still outstanding?

Then:

> What about that Atlas invoice?

Then:

> Wasn't there a payment we couldn't identify?

Then:

> Did we ever figure that one out?

A conventional chat implementation sees something similar to:

```text
message
message
message
message
message
message
```

The model is expected to reconstruct the structure from language.

Toro should instead maintain:

```text
Conversation

    Revenue inquiry
        result = 428,000 MAD

    Outstanding receivables inquiry
        result = 92,000 MAD

        Atlas invoice investigation

            Payment investigation
                TX-4821
```

When the user says:

> Did we ever figure that one out?

the runtime already knows that the current execution context is related to the Atlas invoice and the unresolved transaction.

The LLM is no longer solely responsible for remembering what "that one" means from a long transcript.

---

# 4. The Critical Design Decision: This Is Not Literally a Stack

A CPU call stack is normally a strict Last-In-First-Out structure.

If the stack contains:

```text
A
B
C
```

then `C` must return before `B`, and `B` must return before `A`.

Agent work cannot always obey this constraint.

For example:

```text
Investigate July close
    Investigate TX-100
    Investigate TX-101
    Investigate TX-102
```

Those three investigations may run concurrently.

TX-101 may finish before TX-100.

TX-102 may need to wait until tomorrow for a document.

Therefore, internally, Toro should **not implement the execution model as one Redis LIST containing frames**.

That would be too restrictive.

Instead, the underlying structure should be a **persistent execution tree**.

Each frame has a parent.

The "stack" is simply the currently active path through that tree.

For example:

```text
root conversation
|
+-- revenue inquiry [COMPLETED]
|
+-- outstanding invoices [ACTIVE]
    |
    +-- investigate Atlas [ACTIVE]
    |   |
    |   +-- investigate TX-4821 [ACTIVE]
    |
    +-- investigate ACME [SUSPENDED]
```

The currently active stack is:

```text
root conversation
outstanding invoices
investigate Atlas
investigate TX-4821
```

This distinction should be fundamental to the implementation.

Toro exposes **stack semantics** to agents, while internally storing an **execution tree**.

---

# 5. Core Runtime Objects

The primitive requires two main objects:

```text
ExecutionStack
ExecutionFrame
```

The `ExecutionStack` represents one logical execution.

The `ExecutionFrame` represents one unit of work inside that execution.

A Go representation could start approximately like this:

```go
package execution

import (
	"encoding/json"
	"time"
)

type StackID string
type FrameID string
type TenantID string
type CompanyID string
type AgentID string
type SessionID string

type FrameStatus string

const (
	FrameStatusActive    FrameStatus = "ACTIVE"
	FrameStatusWaiting   FrameStatus = "WAITING"
	FrameStatusSuspended FrameStatus = "SUSPENDED"
	FrameStatusCompleted FrameStatus = "COMPLETED"
	FrameStatusFailed    FrameStatus = "FAILED"
	FrameStatusCancelled FrameStatus = "CANCELLED"
)

type ExecutionStack struct {
	ID StackID `json:"id"`

	TenantID  TenantID  `json:"tenant_id"`
	CompanyID CompanyID `json:"company_id"`
	AgentID   AgentID   `json:"agent_id"`
	SessionID SessionID `json:"session_id"`

	RootFrameID FrameID `json:"root_frame_id"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ExecutionFrame struct {
	ID      FrameID `json:"id"`
	StackID StackID `json:"stack_id"`

	ParentFrameID *FrameID `json:"parent_frame_id,omitempty"`

	TenantID  TenantID  `json:"tenant_id"`
	CompanyID CompanyID `json:"company_id"`
	AgentID   AgentID   `json:"agent_id"`

	Objective string `json:"objective"`

	Status FrameStatus `json:"status"`

	Input json.RawMessage `json:"input,omitempty"`

	LocalState json.RawMessage `json:"local_state,omitempty"`

	ReturnValue json.RawMessage `json:"return_value,omitempty"`

	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}
```

This is intentionally not the entire final schema.

The important part is the shape:

```text
frame
    belongs to stack
    has optional parent
    has objective
    has input
    has local execution state
    has lifecycle status
    can eventually have return value
```

---

# 6. What Exactly Is a Frame?

A frame is the smallest independently executable unit of agent work.

It must answer:

```text
What am I trying to accomplish?

Who asked me to do it?

What information was given to me?

What temporary state have I accumulated?

Am I still running?

What result will I return?
```

Consider:

```text
Objective:
Determine whether TX-4821 corresponds to an outstanding invoice.
```

The frame could contain:

```json
{
  "frame_id": "frm_1004",
  "parent_frame_id": "frm_990",
  "objective": "Determine whether TX-4821 corresponds to an outstanding invoice.",
  "status": "ACTIVE",
  "input": {
    "transaction_id": "TX-4821"
  },
  "local_state": {
    "candidate_invoice_ids": [
      "INV-381",
      "INV-390"
    ]
  }
}
```

The frame should not contain every conversation message that preceded it.

It should contain only execution state specific to this problem.

---

# 7. Frame Inputs Are Function Arguments

A useful way to reason about frames is to map them directly onto function calls.

In Go:

```go
result, err := ReconcileTransaction(ctx, transactionID)
```

The function receives:

```text
transactionID
```

Likewise an agent frame receives:

```json
{
  "transaction_id": "TX-4821"
}
```

If the parent already knows:

```text
company_id
period
transaction_id
candidate_customer_id
```

it can explicitly pass those values to its child.

It should not pass its entire context indiscriminately.

This is equivalent to avoiding a function signature like:

```go
func ReconcileTransaction(everythingInTheEntireProgram interface{})
```

The frame boundary should provide context isolation.

---

# 8. Frame Local State Is Equivalent to Local Variables

Consider:

```go
func reconcile(tx Transaction) Result {
	candidates := findCandidates(tx)
	best := rank(candidates)

	return best
}
```

`candidates` and `best` are temporary variables belonging to that invocation.

The agent equivalent might be:

```json
{
  "candidate_invoice_ids": [
    "INV-381",
    "INV-390",
    "INV-411"
  ],
  "rejected_invoice_ids": [
    "INV-411"
  ],
  "current_best_candidate": "INV-381"
}
```

That state belongs to the frame.

It should live outside the LLM.

The model can disappear after every inference call and the runtime should still know where execution stands.

This is a critical requirement.

Toro must never depend on an LLM maintaining hidden state between invocations.

---

# 9. Creating the Root Stack

When an interaction begins, Toro creates an `ExecutionStack`.

Suppose an email arrives:

```text
What was our July revenue?
```

The ingress layer generates the normal runtime event.

The conversation agent receives the event and creates:

```text
Stack stk_800
```

with a root frame:

```text
Frame frm_801

objective:
Handle the user's bookkeeping request.

input:
message_id
conversation_id
user_message
```

The root frame exists because some execution needs a stable owner.

A stack cannot exist without a root.

Conceptually:

```text
stk_800
|
+-- frm_801
    Handle bookkeeping request
```

---

# 10. What PUSH Actually Means in Toro

Suppose the root frame needs to calculate July revenue.

The frame decides that this is a distinct piece of work.

It calls the execution runtime.

Conceptually:

```go
child, err := stack.Push(ctx, PushRequest{
	ParentFrameID: root.ID,
	Objective:     "Calculate company revenue for July 2026",
	Input: map[string]any{
		"period": "2026-07",
	},
})
```

`Push` must perform several concrete actions.

First, validate that the parent exists.

Second, ensure the parent belongs to the same tenant and stack.

Third, ensure the parent is in a state from which child creation is allowed.

Fourth, create the child frame.

Fifth, persist it.

Sixth, establish the parent-child relationship.

Seventh, emit an event indicating that work was created.

The state becomes:

```text
stk_800
|
+-- frm_801
    Handle bookkeeping request
    |
    +-- frm_802
        Calculate July revenue
```

If `frm_802` is now the work receiving attention, the active execution path is:

```text
frm_801
frm_802
```

That is equivalent to pushing a function invocation onto a normal call stack.

---

# 11. PUSH Should Be a Runtime Operation, Not an LLM Fiction

The LLM may determine:

> I need to investigate this transaction first.

But the LLM should not be trusted to maintain the actual hierarchy.

The model can request something equivalent to:

```json
{
  "operation": "spawn_child_frame",
  "objective": "Investigate TX-4821",
  "input": {
    "transaction_id": "TX-4821"
  }
}
```

The harness validates that request and performs the real state transition.

The persistent runtime is authoritative.

The LLM only proposes work.

---

# 12. What POP Actually Means

Suppose `frm_802` finishes.

It calculated:

```text
428,000 MAD
```

In a normal function:

```go
return 428000
```

For the agent runtime:

```go
err := stack.CompleteFrame(ctx, CompleteFrameRequest{
	FrameID: child.ID,
	ReturnValue: map[string]any{
		"currency": "MAD",
		"revenue":  428000,
	},
})
```

The runtime then:

1. verifies that the frame can be completed;
2. stores its structured return value;
3. changes status from `ACTIVE` to `COMPLETED`;
4. records `completed_at`;
5. persists the completed frame;
6. emits `execution.frame.completed`;
7. makes the result available to the parent;
8. removes the frame from the active execution path.

This is what **POP** means.

But unlike a real process call stack, the object itself is not deleted.

Before:

```text
ACTIVE PATH

frm_801
frm_802
```

After:

```text
ACTIVE PATH

frm_801
```

while the execution tree remains:

```text
frm_801
|
+-- frm_802 [COMPLETED]
    result = 428000 MAD
```

Therefore, "pop" means:

> Remove this frame from active execution because it has returned.

It does **not** mean:

> Delete its history from storage.

---

# 13. How the Parent Receives the Return Value

This should also be explicit.

The parent should not need to reread the child's internal reasoning.

The child returns a structured value.

For example:

```json
{
  "period": "2026-07",
  "recognized_revenue": 428000,
  "currency": "MAD",
  "source_count": 47
}
```

The parent may record this under its local state:

```json
{
  "child_results": {
    "frm_802": {
      "period": "2026-07",
      "recognized_revenue": 428000,
      "currency": "MAD"
    }
  }
}
```

When the parent next invokes an LLM, its context assembler may include:

```text
CHILD RESULT

Objective:
Calculate company revenue for July 2026

Result:
428,000 MAD
```

The parent's prompt does not require the child's complete transcript.

This is equivalent to:

```go
revenue := calculateRevenue()
```

The caller gets the value, not the internals of the called function.

---

# 14. The Active Path

Because the underlying structure is a tree, the runtime must explicitly track which path is currently active for a serial execution branch.

Consider:

```text
frm_root
|
+-- frm_revenue [COMPLETED]
|
+-- frm_outstanding [ACTIVE]
    |
    +-- frm_atlas [ACTIVE]
        |
        +-- frm_tx4821 [ACTIVE]
```

The active path is:

```text
frm_root
frm_outstanding
frm_atlas
frm_tx4821
```

A method should expose this directly:

```go
GetActivePath(ctx, stackID)
```

returning frames ordered from root to current leaf.

This is the actual equivalent of reading the current call stack.

---

# 15. Why Redis Is Useful

Redis should contain the hot representation of currently executable state.

It should not be treated as the sole authoritative record.

Redis is appropriate because operations such as:

```text
Which frame is currently active?

What children are waiting?

Who owns this frame?

Has this frame's lease expired?

What is the active path?
```

must be extremely fast.

One possible Redis structure is:

```text
exec:stack:{stack_id}
exec:frame:{frame_id}
exec:children:{frame_id}
exec:active:{stack_id}
exec:lease:{frame_id}
```

For example:

```text
exec:active:stk_800 = frm_tx4821
```

The frame itself could be stored as a Redis Hash:

```text
HSET exec:frame:frm_tx4821
    stack_id stk_800
    parent_id frm_atlas
    status ACTIVE
    objective "Investigate TX-4821"
```

Larger local state can be serialized as JSON or stored separately.

---

# 16. Why Redis Cannot Be the Source of Truth

Agent execution may last:

```text
seconds
hours
days
weeks
```

Redis can be lost, evicted, restarted, flushed, or reconstructed.

The durable execution tree therefore belongs in ToroDB.

Every meaningful transition should be persisted.

At minimum:

```text
frame created
frame activated
frame suspended
frame resumed
frame completed
frame failed
frame cancelled
```

If Redis disappears, Toro should rebuild hot state from durable execution records.

The architecture is therefore:

```text
ToroDB
durable execution truth

Redis
hot runtime projection
```

This is similar to keeping persistent program state outside the CPU stack because unlike a normal process, agent execution may survive process death.

---

# 17. Suggested Durable Database Schema

A starting PostgreSQL representation might look like:

```sql
CREATE TABLE execution_stacks (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL,
    company_id UUID NOT NULL,
    agent_id UUID NOT NULL,
    session_id UUID,
    root_frame_id UUID,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE execution_frames (
    id UUID PRIMARY KEY,
    stack_id UUID NOT NULL REFERENCES execution_stacks(id),
    parent_frame_id UUID REFERENCES execution_frames(id),

    tenant_id UUID NOT NULL,
    company_id UUID NOT NULL,
    agent_id UUID NOT NULL,

    objective TEXT NOT NULL,

    status TEXT NOT NULL,

    input JSONB,
    local_state JSONB,
    return_value JSONB,

    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    suspended_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ
);

CREATE INDEX execution_frames_stack_idx
    ON execution_frames(stack_id);

CREATE INDEX execution_frames_parent_idx
    ON execution_frames(parent_frame_id);

CREATE INDEX execution_frames_status_idx
    ON execution_frames(stack_id, status);
```

The precise schema can evolve.

The important point is that parent-child relationships must be first-class database relationships, not reconstructed from chat messages.

---

# 18. Suspension

Agent execution differs fundamentally from conventional function execution because work often needs to wait for the outside world.

For example:

```text
Investigate TX-4821
```

may discover:

```text
Required invoice has not yet been uploaded.
```

A normal function may block.

Toro should not keep a worker alive.

Instead:

```go
SuspendFrame(ctx, frameID, reason)
```

changes:

```text
ACTIVE
```

to:

```text
SUSPENDED
```

and persists the reason.

Example:

```json
{
  "status": "SUSPENDED",
  "suspension": {
    "reason_code": "WAITING_FOR_DOCUMENT",
    "document_type": "invoice",
    "counterparty": "Atlas"
  }
}
```

No Go goroutine should remain waiting.

No LLM session must remain alive.

No Redis connection must remain open.

The runtime has serialized the continuation into data.

That is the crucial adaptation of call-stack semantics to agent systems.

---

# 19. Resumption

Later, an invoice arrives.

The document pipeline emits an event.

The runtime identifies that the new document satisfies the suspended frame's waiting condition.

It executes:

```go
ResumeFrame(ctx, frameID)
```

The frame moves:

```text
SUSPENDED
```

to:

```text
ACTIVE
```

The runtime reconstructs the context using:

```text
frame objective
frame input
frame local state
returned child results
new event
RAG context
relevant memory
```

A new model invocation can continue execution.

There is no requirement for the same worker or model instance to resume it.

This should be a hard architectural invariant.

---

# 20. Suspension Is Not POP

This distinction matters.

If a frame is completed:

```text
POP
```

because it has returned a value.

If a frame is suspended:

```text
DO NOT POP
```

because its work is incomplete.

It leaves the active processing set temporarily, but remains logically unresolved.

For example:

```text
Outstanding invoices
|
+-- Atlas investigation [SUSPENDED]
```

The parent needs to know:

```text
Atlas investigation is unfinished.
```

It may continue other work, but it cannot treat the child as having returned a successful result.

---

# 21. Concurrency

Suppose a parent needs to investigate 100 transactions.

It should not necessarily push:

```text
TX-1
    TX-2
        TX-3
            TX-4
```

That would incorrectly represent sequential dependency.

Instead:

```text
Reconcile July
|
+-- TX-1
+-- TX-2
+-- TX-3
+-- TX-4
```

These are sibling frames.

A parent operation should therefore support something like:

```go
SpawnChildren(ctx, parentID, []ChildRequest{...})
```

The runtime can dispatch those frames independently.

Each receives its own execution lease.

The parent can specify a join policy.

For example:

```text
WAIT_ALL
WAIT_ANY
WAIT_QUORUM
DO_NOT_WAIT
```

For bookkeeping reconciliation:

```text
WAIT_ALL
```

will often be appropriate.

---

# 22. The Parent Is Not Necessarily "On Top"

This is where the conventional stack analogy stops being literal.

With concurrent children:

```text
Parent
|
+-- A [ACTIVE]
+-- B [ACTIVE]
+-- C [SUSPENDED]
```

there is no single global top-of-stack.

Each concurrent branch has an active path:

```text
Parent -> A
Parent -> B
```

Therefore the runtime should reason about:

```text
execution tree
active branches
frame ancestry
```

rather than relying on one global LIFO array.

The term **Stack Primitive** is still useful because the semantics experienced by any individual execution branch remain call-stack-like.

---

# 23. Frame Ownership

A frame may be picked up by any worker.

To prevent two workers from executing the same frame simultaneously, active frames need leases.

Example:

```go
type ExecutionLease struct {
	FrameID   FrameID
	WorkerID  string
	LeaseID   string
	ExpiresAt time.Time
}
```

Before executing a frame:

```text
worker-17 claims frm_tx4821
```

Redis may contain:

```text
exec:lease:frm_tx4821
```

with an expiration.

If `worker-17` crashes, the lease expires.

Another worker can claim the frame.

The durable frame itself remains untouched.

This avoids duplicate autonomous execution.

---

# 24. State Mutation Must Be Versioned

There is another concurrency problem.

Suppose two processes read the same frame state.

Both attempt to update it.

The system should use optimistic concurrency.

Each frame should have something like:

```text
version = 7
```

An update says:

```text
Update frame frm_1
only if version = 7
```

The database changes:

```text
version = 8
```

If another worker attempts an update using version 7, it receives a conflict.

This protects frame state from lost updates.

---

# 25. Relationship With the LLM

The model should never receive the entire execution tree.

Before invoking the model, Toro constructs a **Frame Context**.

Example:

```text
SYSTEM
You are the bookkeeping reconciliation agent.

CURRENT OBJECTIVE
Determine whether TX-4821 corresponds to an outstanding invoice.

PARENT OBJECTIVE
Resolve Atlas payment.

FRAME INPUT
transaction_id: TX-4821

LOCAL STATE
Current candidates:
INV-381
INV-390

RETURNED CHILD RESULTS
Counterparty resolution:
Atlas Group SA may pay invoices on behalf of Atlas Distribution.

RETRIEVED CONTEXT
...

LATEST EVENT
...
```

This is what the model reasons over.

The stack primitive therefore constrains attention.

The model does not need to know the 40 unrelated things that happened previously.

---

# 26. The Frame Does Not Store Chain-of-Thought

`local_state` must not become a dump of model reasoning.

Bad:

```text
I first thought invoice 381 might work but then I reconsidered because...
```

Good:

```json
{
  "candidate_invoice_ids": [
    "INV-381",
    "INV-390"
  ],
  "rejected_candidates": {
    "INV-390": "amount_mismatch"
  },
  "best_candidate": "INV-381",
  "confidence": 0.94
}
```

Store execution state.

Do not store hidden reasoning.

---

# 27. The Agent Can Create a Child Because It Needs More Work

Suppose the current frame is:

```text
Resolve TX-4821
```

The LLM determines:

```text
I cannot resolve the transaction until I know whether Atlas Group and Atlas Distribution are related.
```

The harness exposes a tool similar to:

```text
create_subtask(
    objective,
    input,
    expected_output_schema
)
```

The model calls:

```json
{
  "objective": "Determine the business relationship between Atlas Group SA and Atlas Distribution SARL",
  "input": {
    "entity_a": "Atlas Group SA",
    "entity_b": "Atlas Distribution SARL"
  },
  "expected_output": {
    "relationship": "string",
    "confidence": "number"
  }
}
```

The runtime creates a child frame.

The parent may become:

```text
WAITING
```

because it requires that result.

The child becomes:

```text
ACTIVE
```

The runtime executes it.

When the child completes, its structured return value is inserted into the parent.

The parent becomes runnable again.

This is the closest agent equivalent of:

```go
relationship := determineRelationship(a, b)
```

---

# 28. Waiting on Children

A frame should have an explicit distinction between:

```text
ACTIVE
WAITING
SUSPENDED
```

`ACTIVE` means the frame itself can currently execute.

`WAITING` means it is waiting for internal child work.

`SUSPENDED` means it is waiting for an external condition.

Example:

```text
Resolve TX-4821
    status = WAITING

    Determine Atlas relationship
        status = ACTIVE
```

After the child completes:

```text
Resolve TX-4821
    status = ACTIVE

    Determine Atlas relationship
        status = COMPLETED
```

This makes runtime scheduling deterministic.

---

# 29. Scheduler Behavior

The scheduler should operate over runnable frames.

Conceptually:

```text
find frames where:
    status = ACTIVE
    no current valid execution lease
```

Claim one.

Execute it.

The result of execution may be:

```text
COMPLETE
CREATE_CHILD
WAIT
SUSPEND
FAIL
```

The scheduler then applies the corresponding transition.

This makes frame execution behave much more like an interpreter/runtime than an open-ended chat loop.

---

# 30. A Frame Execution Contract

Every frame execution should produce one of a finite set of runtime outcomes.

For example:

```go
type ExecutionOutcomeType string

const (
	OutcomeComplete    ExecutionOutcomeType = "COMPLETE"
	OutcomeCreateChild ExecutionOutcomeType = "CREATE_CHILD"
	OutcomeWait        ExecutionOutcomeType = "WAIT"
	OutcomeSuspend     ExecutionOutcomeType = "SUSPEND"
	OutcomeFail        ExecutionOutcomeType = "FAIL"
)

type ExecutionOutcome struct {
	Type ExecutionOutcomeType

	ReturnValue json.RawMessage

	Child *CreateChildRequest

	Suspension *SuspensionRequest

	Error *FrameError
}
```

The model or deterministic worker produces an outcome.

The runtime interprets it.

The model itself does not mutate Redis or Postgres.

---

# 31. Frame Return Types

A child should declare what it is expected to return.

This is analogous to a typed function signature.

Example:

```text
Objective:
Resolve relationship between two companies.

Expected output:

{
    relationship_type: string
    supported: boolean
    evidence_ids: []string
    confidence: float
}
```

The result must be validated before `CompleteFrame` succeeds.

This prevents children from returning arbitrary prose that parents must interpret.

In Go, expected return schemas may be represented through typed worker contracts or JSON Schema for dynamic agent tasks.

---

# 32. Example Full Flow

User sends:

```text
Did we ever resolve that Atlas payment?
```

The runtime has a conversation root.

It creates:

```text
frm_200

objective:
Answer user's question about the Atlas payment.
```

The agent does not know which payment is referenced.

It creates:

```text
frm_201

parent:
frm_200

objective:
Identify the Atlas payment referenced by the user.
```

`frm_200` becomes:

```text
WAITING
```

`frm_201` becomes:

```text
ACTIVE
```

RAG retrieves recent unresolved Atlas transactions.

The child returns:

```json
{
  "transaction_id": "TX-4821"
}
```

`frm_201` becomes `COMPLETED`.

It is popped from the active branch.

`frm_200` receives:

```text
TX-4821
```

and becomes active again.

It creates another child:

```text
frm_202

objective:
Determine current reconciliation status of TX-4821.
```

The child checks canonical bookkeeping state.

It sees:

```text
still unresolved
candidate INV-381
counterparty ambiguity
```

It creates:

```text
frm_203

objective:
Determine whether Atlas Group SA may pay invoices issued to Atlas Distribution SARL.
```

Current branch:

```text
frm_200
frm_202
frm_203
```

`frm_203` completes:

```json
{
  "relationship": "Atlas Group SA is authorized to settle Atlas Distribution invoices.",
  "confidence": 0.99
}
```

Pop `frm_203`.

The active branch becomes:

```text
frm_200
frm_202
```

`frm_202` resumes with the child result.

It confirms the invoice match.

It completes:

```json
{
  "transaction_id": "TX-4821",
  "status": "MATCHED",
  "invoice_id": "INV-381"
}
```

Pop `frm_202`.

The active branch becomes:

```text
frm_200
```

The root receives the result.

It replies to the user.

Then `frm_200` completes.

The active execution branch is empty.

The execution tree remains stored:

```text
frm_200 [COMPLETED]
|
+-- frm_201 [COMPLETED]
|
+-- frm_202 [COMPLETED]
    |
    +-- frm_203 [COMPLETED]
```

That is the complete stack lifecycle.

---

# 33. How Conversation Continuation Works

Suppose five minutes later the user says:

> Okay, what about ACME?

Do not automatically reopen the previous completed frame.

The conversation router creates a new frame:

```text
frm_204

objective:
Resolve user's ACME follow-up.
```

Its parent may be the persistent conversation root or another conversational owner depending on the broader session model.

RAG can retrieve the previously completed Atlas work if relevant.

The execution stack itself should not be abused as long-term history.

That belongs to retrieval.

---

# 34. Reopening Completed Work

Sometimes the user explicitly returns to completed work.

Example:

> Wait, that Atlas payment was not for INV-381. Check it again.

Toro should not mutate the historical completed frame `frm_202`.

Create:

```text
frm_250

reopened_from:
frm_202

objective:
Re-evaluate reconciliation of TX-4821 after user correction.
```

The old result remains auditable.

The new frame contains the new execution.

This is important for bookkeeping because prior decisions may need to be inspected later.

---

# 35. Cancellation

A parent or user may decide work is no longer required.

Example:

```text
Never mind, don't investigate the other transactions.
```

The runtime may mark appropriate frames:

```text
CANCELLED
```

A cancellation should record:

```text
who cancelled
when
why
```

Active worker leases should be revoked or allowed to expire.

A cancelled frame cannot return a normal successful value.

---

# 36. Failure

A frame can fail because of runtime or domain reasons.

Examples:

```text
tool unavailable
invalid worker response
permission denied
LLM repeatedly failed schema validation
required source unavailable
resource budget exhausted
```

Failure should produce structured state.

Example:

```json
{
  "code": "RETRIEVAL_SOURCE_UNAVAILABLE",
  "message": "Invoice store could not be queried.",
  "retryable": true
}
```

The parent can decide whether to:

```text
retry
use another strategy
suspend
fail itself
continue with partial result
```

---

# 37. Resource Limits

Recursive agent execution can become dangerous without limits.

Consider an agent repeatedly deciding:

```text
I need another subtask.
```

The system needs hard runtime limits.

At stack level:

```text
maximum total frames
maximum concurrent frames
maximum depth
maximum cumulative token budget
maximum cumulative runtime
```

At frame level:

```text
maximum LLM calls
maximum tool calls
maximum retries
maximum child frames
maximum local state size
```

Example configuration:

```go
type StackLimits struct {
	MaxDepth              int
	MaxFrames             int
	MaxConcurrentFrames   int
	MaxChildrenPerFrame   int
	MaxLLMCallsPerFrame   int
	MaxToolCallsPerFrame  int
	MaxLocalStateBytes    int
}
```

If depth exceeds the configured limit, `Push` should fail deterministically.

---

# 38. Recursive Calls

Actual recursion should be possible.

Suppose an agent is traversing an organizational hierarchy:

```text
analyze company
    analyze division
        analyze department
            analyze team
```

Each call can invoke the same frame handler with different input.

This is structurally recursive.

The runtime sees no special case.

It only sees:

```text
frame
    child frame
        child frame
            child frame
```

The depth limit protects against runaway recursion.

---

# 39. DAG Integration

Toro's existing workflow DAG and the Stack Primitive solve different problems.

A DAG says:

```text
document_readiness
        |
        v
bank_classifier
        |
        v
customer_reconciler
        |
        v
compliance_router
```

That is the predefined workflow structure.

The stack describes the dynamic work created while executing one node.

For example:

```text
DAG NODE:
customer_reconciler

Execution Frame:
Reconcile TX-4821
|
+-- Resolve customer identity
|
+-- Search candidate invoices
|
+-- Verify amount tolerance
|
+-- Resolve Atlas relationship
```

Therefore:

> DAG defines allowed macro execution flow.

> Stack defines dynamic micro execution context.

A DAG node can create one or more root frames for its internal work.

---

# 40. NATS Integration

Frame state should not be transmitted in full through every NATS message.

Messages should primarily contain references.

Example:

```json
{
  "event_type": "execution.frame.runnable",
  "stack_id": "stk_800",
  "frame_id": "frm_202"
}
```

The worker loads current frame state from the runtime.

Completion event:

```json
{
  "event_type": "execution.frame.completed",
  "stack_id": "stk_800",
  "frame_id": "frm_202",
  "parent_frame_id": "frm_200"
}
```

The parent scheduler can then become runnable.

This avoids copying large execution state through NATS repeatedly.

---

# 41. Transactional Consistency

A critical implementation issue is avoiding situations like:

```text
Postgres says ACTIVE
Redis says COMPLETED
NATS says RUNNABLE
```

Toro needs a consistent transition strategy.

A good model is:

1. durable state transition occurs transactionally in ToroDB;
2. an outbox event is written in the same database transaction;
3. the outbox publisher emits the corresponding NATS event;
4. Redis is updated as a hot projection;
5. if Redis becomes inconsistent, it can be reconstructed from durable state.

Therefore, correctness should depend on Postgres/ToroDB, not Redis.

---

# 42. Why Not Just Use Temporal or Goroutines?

The primitive is not merely a job scheduler.

A goroutine provides process-local concurrency.

It does not naturally provide:

```text
durable semantic objective
parent-child agent context
LLM context isolation
multi-day suspension
structured return values
retrievable execution history
model-independent continuation
```

Likewise workflow engines can provide durable orchestration, but the Stack Primitive is the semantic execution model presented to agents.

The underlying scheduler could eventually use workflow-engine ideas, but Toro still needs its own frame abstraction.

---

# 43. Stack Context and Memory Must Remain Separate

Suppose a frame discovers:

```text
Atlas Group often pays Atlas Distribution invoices.
```

That fact exists initially inside execution state.

When the frame completes, Memory Selection may decide to persist it.

But Stack itself should not suddenly treat every local result as permanent knowledge.

The lifecycle is:

```text
FRAME
discovers something
    |
    v
FRAME COMPLETES
    |
    v
MEMORY SELECTOR
evaluates result
    |
    v
MEMORY
possibly persists it
```

This prevents execution state from becoming an uncontrolled memory database.

---

# 44. Stack Context and RAG Must Remain Separate

The stack knows:

```text
I am investigating TX-4821.
```

It does not need to contain:

```text
every invoice
every customer relationship
every historical bank transaction
every accounting SOP
```

Before execution, RAG retrieves the information the frame requires.

Therefore:

```text
Frame = problem state

RAG = contextual evidence

Memory = learned historical knowledge
```

The LLM input is assembled from all three.

---

# 45. Context Assembly Algorithm

At execution time, the context assembler should approximately do:

```text
1. Load current frame.

2. Walk immediate ancestry:
   current parent
   optionally grandparent if needed.

3. Load structured child results already returned.

4. Load relevant local state.

5. Ask RAG for external context.

6. Ask RAG for relevant Memory.

7. Add latest triggering event.

8. Enforce token budget.

9. Construct model input.

10. Invoke model.
```

It should not automatically traverse the entire execution tree.

---

# 46. Frame Compaction

A long-running frame can accumulate too much local state.

Example:

```text
50 candidate invoices examined
47 rejected
3 still plausible
```

The active state does not need full model outputs for all 47 rejected invoices.

It might store:

```json
{
  "remaining_candidates": [
    "INV-381",
    "INV-390",
    "INV-412"
  ],
  "rejected_count": 47,
  "rejection_summary": {
    "amount_mismatch": 21,
    "date_mismatch": 17,
    "customer_mismatch": 9
  }
}
```

Detailed audit records can remain elsewhere.

This prevents active frame state from growing indefinitely.

---

# 47. Observability

Because frames are explicit, Toro gains a powerful execution trace.

For any agent request, it should be possible to inspect:

```text
What root objective was created?

Which children were spawned?

How deep did execution go?

Which frames suspended?

Which frame failed?

How long did each frame run?

What did each child return?

How many LLM calls happened?

How many tokens were consumed?

How many tool calls occurred?
```

A trace may look like:

```text
Handle bookkeeping query               1.8s
|
+-- Identify Atlas transaction          120ms
|
+-- Resolve transaction                 1.4s
    |
    +-- Resolve company relationship    700ms
```

This is far more debuggable than a single giant agent loop.

---

# 48. V1 Go Interfaces

A practical V1 interface could resemble:

```go
type StackStore interface {
	CreateStack(
		ctx context.Context,
		req CreateStackRequest,
	) (*ExecutionStack, error)

	GetStack(
		ctx context.Context,
		stackID StackID,
	) (*ExecutionStack, error)

	CreateFrame(
		ctx context.Context,
		req CreateFrameRequest,
	) (*ExecutionFrame, error)

	GetFrame(
		ctx context.Context,
		frameID FrameID,
	) (*ExecutionFrame, error)

	CompleteFrame(
		ctx context.Context,
		req CompleteFrameRequest,
	) (*ExecutionFrame, error)

	SuspendFrame(
		ctx context.Context,
		req SuspendFrameRequest,
	) (*ExecutionFrame, error)

	ResumeFrame(
		ctx context.Context,
		frameID FrameID,
	) (*ExecutionFrame, error)

	FailFrame(
		ctx context.Context,
		req FailFrameRequest,
	) (*ExecutionFrame, error)

	CancelFrame(
		ctx context.Context,
		req CancelFrameRequest,
	) (*ExecutionFrame, error)

	ListChildren(
		ctx context.Context,
		parentFrameID FrameID,
	) ([]ExecutionFrame, error)

	GetAncestors(
		ctx context.Context,
		frameID FrameID,
	) ([]ExecutionFrame, error)

	GetRunnableFrames(
		ctx context.Context,
		limit int,
	) ([]ExecutionFrame, error)
}
```

The actual Go service should separate:

```text
domain logic
repository
Redis projection
NATS publisher
scheduler
LLM executor
```

rather than putting all behavior into a single stack service.

---

# 49. Proposed Package Boundaries

A likely Go package layout:

```text
internal/execution/
    model.go
    service.go
    transitions.go
    errors.go

internal/execution/store/
    postgres.go
    redis.go

internal/execution/scheduler/
    scheduler.go
    lease.go

internal/execution/events/
    publisher.go
    types.go

internal/execution/context/
    assembler.go

internal/execution/worker/
    executor.go
```

Responsibilities:

`execution` defines stack semantics.

`store` persists them.

`scheduler` decides what can run.

`events` integrates NATS.

`context` creates LLM execution context.

`worker` invokes deterministic or LLM-based handlers.

This should remain independent from bookkeeping-specific logic.

---

# 50. State Transition Rules

Transitions should be deterministic.

At minimum:

```text
ACTIVE -> WAITING
ACTIVE -> SUSPENDED
ACTIVE -> COMPLETED
ACTIVE -> FAILED
ACTIVE -> CANCELLED

WAITING -> ACTIVE
WAITING -> FAILED
WAITING -> CANCELLED

SUSPENDED -> ACTIVE
SUSPENDED -> CANCELLED
SUSPENDED -> FAILED
```

Invalid transitions should fail.

For example:

```text
COMPLETED -> ACTIVE
```

must not happen.

To revisit completed work, create a new frame using `reopened_from`.

This preserves execution history.

---

# 51. Runtime Invariants

The implementation should enforce these invariants.

Every frame belongs to exactly one stack.

Every non-root frame has exactly one parent.

A parent and child must belong to the same tenant and stack.

Completed frames are immutable.

A frame cannot complete with an invalid return value.

A parent waiting for a child cannot resume until its join condition is satisfied.

A suspended frame does not consume a worker.

Only a worker holding the current execution lease may perform the active transition.

All durable transitions must be recoverable after process failure.

The entire active execution context must be reconstructable without relying on previous model state.

---

# 52. What V1 Should Actually Build

V1 should not attempt to solve every form of agent orchestration.

Build the following concrete capabilities.

Create a durable stack.

Create root frames.

Create child frames.

Persist parent-child relationships.

Maintain deterministic frame statuses.

Allow structured frame input.

Allow structured frame local state.

Allow validated structured return values.

Support `ACTIVE`, `WAITING`, `SUSPENDED`, `COMPLETED`, `FAILED`, and `CANCELLED`.

Support children returning values to parents.

Support suspension without holding workers.

Support resumption from durable state.

Support multiple sibling children.

Support frame leases.

Maintain a Redis hot-state projection.

Persist authoritative state in ToroDB.

Emit NATS lifecycle events.

Construct an active-path context for the LLM.

Enforce depth and resource limits.

Provide execution tracing.

Do not put Memory or RAG implementation inside this package.

---

# 53. Final Mental Model

The simplest way to understand the primitive is to compare ordinary Go execution with Toro execution.

A Go program may do:

```go
func answerRevenueQuestion(period string) Answer {
	revenue := calculateRevenue(period)

	outstanding := calculateOutstanding(period)

	return Answer{
		Revenue:     revenue,
		Outstanding: outstanding,
	}
}
```

The Go runtime internally maintains stack frames.

Toro's equivalent is:

```text
FRAME: Answer revenue question
|
+-- FRAME: Calculate revenue
|       returns 428,000 MAD
|
+-- FRAME: Calculate outstanding
        returns 92,000 MAD
```

The difference is that Toro's frames are:

```text
durable
distributed
asynchronous
inspectable
suspendable
recoverable
LLM-independent
```

A normal call stack exists only while the process is alive.

Toro's execution stack should survive:

```text
worker death
model invocation ending
server restart
deployment
hours of inactivity
waiting for external documents
```

That is the core primitive.

The runtime should be able to kill every model process and every worker involved in an execution, restart the system, load a frame from storage, and still know:

```text
what the agent was doing,
why it was doing it,
what inputs it had,
what work it had delegated,
what children had returned,
what it was waiting for,
and what result it eventually needs to return to its caller.
```

If Toro can guarantee that property, then the Stack Primitive is doing for agent execution what the call stack does for normal program execution, but adapted to durable autonomous work.
