# Product Requirements Document: Agent Memory Primitive

## 1. Overview

The **Agent Memory Primitive** is the durable knowledge layer of the agent runtime.

Its responsibility is to answer:

> **What does this agent or company already know that should remain useful beyond the current execution?**

Memory is separate from both:

* the **Stack**, which stores current execution state;
* **RAG**, which decides what stored information should be retrieved for a particular execution.

Memory is therefore not chat history and not the retrieval mechanism itself.

It is the durable representation of learned state.

---

## 2. Problem

Agents operating over long periods need continuity.

Without memory, every interaction effectively starts from zero unless the entire history is repeatedly injected into the model.

That creates several problems:

* repeated reasoning over facts already established;
* repeated entity resolution;
* repeated corrections;
* repeated user explanations;
* unnecessary token usage;
* inconsistent decisions between sessions;
* inability to learn operational patterns;
* inability to retain company-specific conventions;
* large prompts containing irrelevant historical information.

For example, if an accountant repeatedly establishes that:

```text
"CM CASA 1938" on the bank statement refers to
"Crédit Mutuel Casablanca"
```

the agent should not rediscover this relationship every time.

Similarly, if a company consistently classifies a particular supplier in a certain way, the system should retain that learned operational fact.

---

# 3. Goals

The Memory primitive must:

1. Persist useful knowledge beyond a single execution.
2. Distinguish temporary working memory from durable memory.
3. Represent memories as structured, independently addressable objects.
4. Track provenance for every memory.
5. Track confidence and validity.
6. support memory updates and supersession.
7. avoid treating raw conversations as durable knowledge.
8. support company-specific learning.
9. support agent-specific and user-specific memory where appropriate.
10. expose memories for retrieval through the RAG primitive.
11. preserve auditability.
12. prevent old or incorrect memories from silently influencing future decisions.

---

# 4. Non-Goals

The Memory primitive does not:

* decide the current objective;
* maintain active execution hierarchy;
* perform semantic retrieval itself;
* send everything stored in memory to the LLM;
* replace canonical business databases;
* replace accounting ledgers;
* replace CRM or ERP records;
* treat model-generated speculation as fact;
* store hidden chain-of-thought reasoning.

---

# 5. Core Distinction

The architecture should distinguish:

```text
STACK
Current execution state.

MEMORY
Knowledge retained beyond execution.

RAG
Selection of relevant stored information.
```

Example:

```text
STACK:
Investigate transaction TX-481

MEMORY:
Supplier "AMZN EU" generally maps to Amazon Business EU.

RAG:
Retrieve that memory because TX-481 contains "AMZN EU".
```

---

# 6. Two Memory Horizons

The system should initially support two explicit memory horizons:

```text
WORKING MEMORY
LONG-TERM MEMORY
```

## 6.1 Working Memory

Working memory contains information that should survive individual frames but may not deserve indefinite persistence.

Examples:

```text
The accountant is currently closing July.

The company is resolving several Atlas invoices.

The user requested Sage 100 output for this current closing cycle.

TX-4821 is still awaiting clarification.
```

Working memory is:

* relatively short-lived;
* highly mutable;
* closely associated with active work;
* allowed to expire.

Redis is the natural primary storage layer.

Durable checkpointing may optionally exist in ToroDB for recovery.

---

## 6.2 Long-Term Memory

Long-term memory contains knowledge expected to remain useful across future execution.

Examples:

```text
Atlas Group SA often pays invoices issued to Atlas Distribution SARL.

Bank descriptor "AWS EMEA" maps to Amazon Web Services.

This accountant prefers unresolved reconciliation notes in French.

Supplier X should normally be mapped to account 6131.

Customer Y usually pays several invoices in a single transfer.
```

Long-term memory belongs in durable ToroDB storage.

---

# 7. Memory Is Not Canonical State

A critical architectural distinction must exist between:

```text
FACTUAL COMPANY STATE
and
AGENT MEMORY
```

For example:

```text
Invoice INV-381 amount = 14,200 MAD
```

should remain canonical invoice data.

Memory should not duplicate that fact unnecessarily.

Memory might instead contain:

```text
Atlas frequently combines multiple invoices into one bank transfer.
```

The source of truth for transactions, invoices, customers, vendors, journal entries, and ledger balances remains the appropriate canonical datastore.

Memory stores **useful learned context about that state**.

---

# 8. Memory Categories

V1 should support a constrained set of categories.

Suggested categories:

```text
ENTITY_ALIAS
USER_PREFERENCE
COMPANY_CONVENTION
OPERATIONAL_PATTERN
RESOLUTION
CORRECTION
RELATIONSHIP
TEMPORARY_CONTEXT
```

Additional categories may later be introduced.

---

# 9. Example Memories

## Entity alias

```json
{
  "type": "ENTITY_ALIAS",
  "subject": "bank_descriptor:ATLAS GRP 00219",
  "predicate": "maps_to",
  "object": "company:atlas_group_sa"
}
```

## Company convention

```json
{
  "type": "COMPANY_CONVENTION",
  "subject": "supplier:aws_emea",
  "predicate": "default_expense_account",
  "object": "61335"
}
```

## Operational pattern

```json
{
  "type": "OPERATIONAL_PATTERN",
  "subject": "customer:atlas_distribution",
  "predicate": "payment_behavior",
  "object": "frequently_batches_multiple_invoices"
}
```

## User preference

```json
{
  "type": "USER_PREFERENCE",
  "subject": "user:accountant_42",
  "predicate": "preferred_export_format",
  "object": "sage_100_pnm"
}
```

---

# 10. Memory Object

Conceptual schema:

```text
Memory {
    memory_id

    tenant_id
    company_id

    scope
    type

    subject
    predicate
    object

    content

    confidence
    importance

    source
    provenance

    status

    valid_from
    valid_until

    created_at
    updated_at
    last_accessed_at

    access_count

    supersedes
    superseded_by
}
```

---

# 11. Memory Scope

Every memory must have an explicit scope.

Suggested scopes:

```text
USER
AGENT
COMPANY
WORKFLOW
DOMAIN
```

Examples:

### USER

```text
Accountant prefers French explanations.
```

### AGENT

```text
Bookkeeping agent learned a recurring reconciliation pattern.
```

### COMPANY

```text
Atlas Group pays invoices for Atlas Distribution.
```

### WORKFLOW

```text
During the July close, use the revised reconciliation policy.
```

### DOMAIN

Potential future shared knowledge such as:

```text
Moroccan bank descriptor conventions.
```

Domain-level memory should have stricter governance because it can affect many tenants.

---

# 12. Memory Provenance

Every durable memory must answer:

> **Why do we believe this?**

Required provenance should include references such as:

```text
source_frame_id
source_transaction_id
source_document_id
source_user_message_id
source_tool_result_id
created_by_agent_id
confirmed_by_user
```

Example:

```json
{
  "memory": "Atlas Group pays invoices for Atlas Distribution",
  "provenance": {
    "frame_id": "frm_992",
    "transaction_ids": [
      "TX-481",
      "TX-729",
      "TX-881"
    ],
    "confirmed_by_user": true
  }
}
```

No important memory should exist without traceable evidence.

---

# 13. Confidence

Memories should carry confidence separately from importance.

Example:

```text
confidence = 0.98
importance = 0.62
```

Confidence means:

> How strongly is this memory supported?

Importance means:

> How useful is this information likely to be in future execution?

These are different concepts.

---

# 14. Memory Status

Suggested lifecycle:

```text
CANDIDATE
ACTIVE
SUPERSEDED
INVALIDATED
EXPIRED
```

### CANDIDATE

Memory has been proposed but not yet accepted.

### ACTIVE

Memory may be retrieved.

### SUPERSEDED

A newer memory replaced it.

### INVALIDATED

Evidence has shown it to be incorrect.

### EXPIRED

Its configured validity period has passed.

---

# 15. Memory Selection

Not everything that happens in execution should become memory.

The system therefore requires a **Memory Selector**.

Its responsibility is:

> Determine whether something produced during execution deserves retention beyond that execution.

Conceptually:

```text
STACK FRAME
     |
     | completion / meaningful update
     v
MEMORY SELECTOR
     |
     + discard
     |
     + working memory
     |
     + long-term candidate
     |
     + update existing memory
     |
     + invalidate existing memory
```

---

# 16. Memory Selection Inputs

The selector should inspect structured execution outputs rather than entire hidden model reasoning.

Signals may include:

```text
frame objective
frame result
entities involved
user corrections
tool results
confidence
repetition
business impact
explicit user instruction
existing memories
```

---

# 17. Memory Selection Policy

The selector should ask several questions.

### Novelty

Is this already known?

```text
existing memory:
Atlas Group pays Atlas Distribution invoices

new observation:
Atlas Group paid another Atlas Distribution invoice
```

This may strengthen the existing memory rather than create another one.

---

### Future utility

Is this likely to matter again?

```text
"The invoice PDF has 3 pages."
```

Probably discard.

```text
"Supplier ACME always includes freight inside invoice total."
```

Potential long-term memory.

---

### Stability

Is the information expected to remain true?

```text
"The accountant is currently closing July."
```

Working memory.

```text
"The accountant prefers Sage PNM exports."
```

Long-term memory.

---

### Confidence

Was this inferred weakly or established reliably?

A single ambiguous observation should generally not become strong durable memory.

---

### Scope

Who should know this?

```text
user
agent
company
workflow
```

---

### Risk

Would an incorrect memory materially affect future execution?

Accounting classification memories should have stricter thresholds than conversational preferences.

---

# 18. Memory Promotion

The system should support promotion:

```text
execution state
    |
    v
working memory
    |
    v
long-term memory
```

Example:

First observation:

```text
Atlas Group paid Atlas Distribution invoice.
```

Working memory.

Second observation:

```text
Another Atlas Group payment matched Atlas Distribution.
```

Pattern confidence increases.

Third confirmation from accountant:

```text
"Yes, Atlas Group is their parent company and pays for them."
```

Promote:

```text
LONG-TERM MEMORY

Atlas Group SA may settle invoices issued to Atlas Distribution SARL.
```

---

# 19. Explicit User Corrections

User corrections should be high-value memory events.

Example:

```text
Agent:
This transaction appears to be office supplies.

Accountant:
No, payments to Meditel under this descriptor are always telecom expenses.
```

The system should generate a candidate memory:

```text
descriptor Meditel X
maps_to
telecommunications expense
```

with strong provenance tied to the user's correction.

---

# 20. Reinforcement

Repeated evidence should strengthen existing memories rather than create duplicates.

Conceptually:

```text
memory confidence
     +
new supporting evidence
     =
updated confidence
```

The exact scoring model should remain implementation-specific.

V1 may use simple deterministic rules.

---

# 21. Contradictions

New evidence may conflict with existing memory.

Example:

Existing:

```text
ACME SARL -> account 6111
```

New accountant correction:

```text
ACME should now be classified under account 6122.
```

Do not overwrite the historical record silently.

Create:

```text
Memory B
supersedes Memory A
```

Memory A becomes:

```text
SUPERSEDED
```

Memory B becomes:

```text
ACTIVE
```

---

# 22. Temporal Memory

Some memories are only valid during a specific period.

Example:

```text
During FY2026, Atlas invoices should use project code CASA-44.
```

Memory must support:

```text
valid_from
valid_until
```

RAG should respect those boundaries during retrieval.

---

# 23. Memory Deduplication

Before creating a memory, the selector should search for:

```text
identical memory
semantically equivalent memory
conflicting memory
more general memory
more specific memory
```

Example:

Candidate:

```text
AWS EMEA is Amazon Web Services
```

Existing:

```text
Bank descriptor "AMAZON AWS EMEA" maps to Amazon Web Services.
```

The system should normally merge or update rather than create unnecessary duplication.

---

# 24. Memory Granularity

Memories should be small and composable.

Avoid:

```text
The accountant explained that Atlas is a customer and they often pay late
and sometimes their parent company pays invoices and there was a problem
with invoice INV-338 last month...
```

Prefer:

```text
Atlas Distribution is a customer.

Atlas Group SA may pay Atlas Distribution invoices.

Atlas Distribution frequently pays after due date.
```

Atomic memories improve retrieval and updating.

---

# 25. Structured and Semantic Representation

Each memory should have both:

```text
structured representation
semantic text representation
```

Example:

Structured:

```json
{
  "subject": "atlas_group_sa",
  "predicate": "pays_for",
  "object": "atlas_distribution_sarl"
}
```

Semantic representation:

```text
Atlas Group SA may settle invoices issued to Atlas Distribution SARL.
```

The structured representation supports deterministic reasoning.

The semantic representation supports embedding-based retrieval.

---

# 26. Embeddings

Durable memories should generally have embeddings generated from their semantic representation.

Embeddings may be stored with ToroDB's vector capabilities.

Conceptually:

```text
memory record
    |
    + structured fields
    |
    + semantic content
    |
    + embedding
```

The embedding is an index, not the memory itself.

---

# 27. Redis Role

Redis should primarily hold working memory.

Example:

```text
session:{session_id}:working_memory
company:{company_id}:active_context
agent:{agent_id}:working_memory
```

Working-memory entries should support TTL.

Example:

```text
closing_period = 2026-07
TTL = 14 days
```

Some entries may be promoted before expiry.

---

# 28. ToroDB Role

ToroDB should store durable memories.

Suggested logical tables:

```text
memories
memory_evidence
memory_relationships
memory_versions
```

A minimal V1 may begin with:

```text
memories
memory_evidence
```

---

# 29. Memory Evidence

Evidence should be stored independently from the memory itself.

Example:

```text
MEMORY

Atlas Group may pay Atlas Distribution invoices.
```

Evidence:

```text
TX-481
TX-772
accountant confirmation message 938
```

This allows confidence to change as evidence accumulates.

---

# 30. Memory Access

The Memory service should expose deterministic access methods.

Conceptual API:

```text
CreateCandidateMemory
PromoteMemory
GetMemory
UpdateMemory
InvalidateMemory
SupersedeMemory
ListEvidence
AttachEvidence
SearchMemory
GetWorkingMemory
SetWorkingMemory
DeleteWorkingMemory
```

Semantic retrieval should eventually be routed through the RAG primitive, not directly through application code.

---

# 31. Integration With Stack

The Stack should generate memory-selection events.

For example:

```text
execution.frame.completed
```

Memory Selector receives:

```text
frame objective
structured output
important tool results
user corrections
entities
```

It then decides whether anything should be retained.

Example:

```text
Frame:
Resolve Atlas transaction

Result:
Atlas Group paid Atlas Distribution invoice.

Memory selector:
Potential recurring relationship detected.

Action:
Create candidate memory.
```

The Stack itself does not persist that knowledge.

---

# 32. Integration With RAG

RAG reads from Memory.

Conceptually:

```text
STACK

"I need to reconcile TX-491"
       |
       v

RAG

"Which memories are relevant?"
       |
       v

MEMORY

Atlas Group pays Atlas Distribution
ACME descriptor mapping
AWS classification
...
```

RAG selects only the relevant subset.

Memory should never independently inject itself into prompts.

---

# 33. Memory Selection Trigger Points

Memory selection should not run indiscriminately after every token or tool invocation.

Recommended triggers:

```text
frame completion
frame suspension
explicit user correction
explicit "remember this"
repeated pattern threshold reached
canonical state change
high-value decision
```

This reduces unnecessary memory writes and LLM calls.

---

# 34. Deterministic Versus LLM Selection

Memory selection should use a hybrid architecture.

## Deterministic rules

Examples:

```text
Explicit user preference -> candidate memory

User correction -> candidate memory

Repeated entity resolution -> candidate memory

Temporary closing period -> working memory
```

## LLM judgment

Useful for questions such as:

```text
Is this fact generalizable?

What atomic memories are contained in this result?

Is this likely to matter later?

Does this contradict an existing memory?
```

The LLM proposes.

The runtime controls persistence.

---

# 35. Risk Classes

Memories should be classified by operational risk.

Example:

```text
LOW
User prefers CSV.

MEDIUM
Customer frequently pays late.

HIGH
Transactions from descriptor X should use account 6131.
```

Higher-risk memories should require:

```text
greater confidence
stronger evidence
or explicit confirmation
```

before being automatically applied.

---

# 36. Memory Application Policy

Retrieving a memory does not automatically make it authoritative.

Each memory may include an application policy:

```text
INFORMATIONAL
SUGGESTIVE
DEFAULT
AUTHORITATIVE
```

Example:

```text
Customer usually pays invoices in batches.
```

INFORMATIONAL.

```text
Accountant explicitly configured AWS expenses to account 61335.
```

DEFAULT or AUTHORITATIVE depending on system policy.

---

# 37. Expiration and Decay

Not all memories remain equally useful forever.

Working memory should use TTL.

Long-term memory may use:

```text
validity dates
usage statistics
confidence decay
manual invalidation
```

V1 does not require sophisticated decay models.

However, the schema should allow them later.

---

# 38. Usage Metadata

Memory records should track:

```text
last_accessed_at
access_count
last_confirmed_at
```

This allows future memory maintenance.

For example:

```text
never retrieved for 3 years
+
weak confidence
+
no supporting evidence
```

may eventually justify archival.

---

# 39. Memory Consolidation

Multiple low-level observations can eventually be consolidated.

Example:

```text
Observation 1:
Atlas payment covered INV-10 and INV-11.

Observation 2:
Atlas payment covered INV-18 and INV-19.

Observation 3:
Atlas payment covered INV-32 and INV-33.
```

Consolidated memory:

```text
Atlas frequently batches several invoices into one payment.
```

The original evidence remains linked.

---

# 40. Memory Compression

Do not preserve every historical detail inside active memory.

Over time:

```text
many individual observations
        |
        v
generalized memory
```

The generalized memory points back to supporting evidence.

This allows memory to scale without indefinitely growing prompt context.

---

# 41. Security and Tenant Isolation

Every memory must be tenant-scoped.

Required:

```text
tenant_id
company_id
scope
visibility
```

No memory may be retrieved across tenant boundaries unless explicitly part of a shared domain knowledge system.

---

# 42. Deletion

Memory architecture must support deletion.

Deletion may be required because of:

```text
user request
company policy
data retention policy
legal requirement
incorrect memory
```

Deleting a memory should also remove or detach its semantic index according to retention policy.

---

# 43. Auditability

For any memory influencing agent behavior, Toro should eventually be able to answer:

```text
What did the agent remember?

Why did it remember it?

When was it learned?

What evidence supported it?

Who confirmed it?

Was it later changed?

Which execution used it?
```

This is especially important in accounting and other high-consequence domains.

---

# 44. Example Lifecycle

Accountant says:

```text
Payments showing "ATLAS HOLDING" are usually payments for Atlas Distribution.
```

Stack processes the statement.

Memory selector receives the correction.

It generates:

```text
type:
RELATIONSHIP

subject:
Atlas Holding

predicate:
may_pay_for

object:
Atlas Distribution
```

Provenance:

```text
user confirmation
frame frm_9982
```

Risk:

```text
MEDIUM
```

Confidence:

```text
0.97
```

Memory becomes active.

Three weeks later, the agent sees:

```text
ATLAS HOLDING
14,200 MAD
```

RAG retrieves the memory.

The reconciliation frame receives:

```text
Potential relationship:
Atlas Holding may pay invoices issued to Atlas Distribution.
```

The agent considers Atlas Distribution invoices as candidates.

The memory improves reasoning without replacing evidence-based reconciliation.

---

# 45. Example of Working Memory

User:

```text
We're closing July. Ignore August transactions for now.
```

Memory selector creates:

```text
scope:
WORKFLOW

type:
TEMPORARY_CONTEXT

content:
Current bookkeeping close concerns July 2026 only.

TTL:
7 days
```

Stored primarily in Redis.

After the close completes, the memory expires.

It never becomes long-term company knowledge.

---

# 46. Example of Supersession

Existing memory:

```text
Supplier Maroc Telecom -> account 6134
```

Accountant later says:

```text
Starting this year, put those costs under 6145.
```

New memory:

```text
valid_from:
2027-01-01

Supplier Maroc Telecom -> account 6145
```

Old memory remains historically valid for earlier periods.

The system must not simply overwrite it.

---

# 47. V1 Memory Selection Heuristics

V1 should intentionally be conservative.

Promote information when at least one of these conditions exists:

1. User explicitly asks the agent to remember it.
2. User corrects the agent.
3. Same resolution occurs repeatedly.
4. A stable entity relationship is confirmed.
5. A stable company convention is discovered.
6. A stable user preference materially affects future interaction.
7. A recurring operational pattern is supported by multiple observations.

Default to discard for:

```text
one-off calculations
temporary tool errors
transient search results
ordinary conversation
raw model reasoning
facts easily available from canonical state
```

---

# 48. Memory Quality Principle

A useful memory should ideally be:

```text
atomic
relevant
stable
traceable
scoped
updatable
retrievable
```

The system should prefer:

```text
100 high-quality memories
```

over:

```text
100,000 loosely summarized conversation fragments
```

Memory quality is more important than memory quantity.

---

# 49. Observability

Track at minimum:

```text
memories created
candidate memories rejected
memories promoted
memories superseded
memories invalidated
retrieval frequency
memory age
memory confidence
memory application rate
user corrections caused by memory
```

Eventually Toro should be able to measure:

```text
Did memory improve task accuracy?

Did it reduce tool calls?

Did it reduce tokens?

Did it introduce errors?

Which memories are most valuable?
```

---

# 50. V1 Scope

V1 should implement:

* working memory in Redis;
* long-term memory in ToroDB;
* atomic memory records;
* scope;
* category;
* semantic content;
* structured subject/predicate/object where possible;
* confidence;
* provenance;
* evidence references;
* ACTIVE / SUPERSEDED / INVALIDATED statuses;
* memory candidate creation;
* deterministic promotion rules;
* LLM-assisted extraction;
* embeddings for durable memories;
* basic deduplication;
* explicit user corrections;
* temporal validity;
* Stack lifecycle integration.

V1 should not attempt:

* autonomous global knowledge graphs;
* sophisticated biological-memory simulation;
* complex reinforcement-learning memory policies;
* cross-company learned memory;
* automatic irreversible accounting decisions based purely on memory;
* aggressive automatic generalization.

---

# 51. Core Architectural Principle

The fundamental principle is:

> **Memory stores learned state, not execution history.**

The runtime should not ask:

> What conversations have we had?

It should ask:

> What did we learn from those conversations and executions that remains useful now?

This distinction transforms memory from a transcript archive into operational knowledge.

Combined with the Stack and RAG primitives:

```text
STACK
What am I doing?

MEMORY
What do I know?

RAG
What do I need to remember right now?
```

Together, these primitives allow an agent to maintain continuity without carrying its entire history inside the model context.
