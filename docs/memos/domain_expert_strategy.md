# BOARD MEMORANDUM

## Toro Platform Strategy: Domain Experts First

**Date:** August 7, 2026
**Decision:** Prioritize domain experts as the first builders on Toro

### Executive Summary

Toro's long-term objective is to build an **agent-to-agent network** in which autonomous agents representing businesses can perform work, exchange information, verify facts, and transact with one another.

To reach that objective, we need to bootstrap the network with real autonomous workers performing real economic work.

The Board has therefore decided to **prioritize domain experts before developers**.

This does not mean abandoning developers or the future Toro SDK. It means reversing the sequencing.

Rather than beginning by building a developer platform and waiting for developers to discover valuable applications, Toro will first build an expert-facing platform that allows people with deep domain knowledge to encode their expertise into autonomous workflows and agents.

Initially, this process will be deliberately high-touch. Toro will identify individual domain experts, work directly with them, help them encode their knowledge, observe where the product fails, and use those interactions to refine the platform.

We will deliberately **do things that do not scale** in order to discover the scalable product.

---

# 1. The Strategic Objective

Toro is not ultimately being built as an application marketplace.

The applications we build are the mechanism through which we bootstrap the larger system.

The strategic objective is:

> **A network of autonomous agents that can represent businesses, perform real work, exchange verified information, and interact economically with other agents.**

For that network to become valuable, the agents must have real capabilities.

An accounting agent must understand accounting.

An insurance agent must understand insurance.

A logistics agent must understand logistics.

A procurement agent must understand procurement.

The critical resource is therefore not simply software development capacity.

It is **domain knowledge**.

---

# 2. Why Experts Come Before Developers

Software developers are capable of building applications, interfaces, integrations, and infrastructure.

However, deep knowledge work cannot be reliably created simply by giving a developer access to an LLM and an SDK.

The difficult part is understanding:

* how professionals actually perform the work
* which rules apply
* which evidence is required
* what exceptions look like
* which decisions require judgment
* when two pieces of information conflict
* when an action is permissible
* when a case must be escalated
* what constitutes a correct result

This knowledge is held by domain experts.

A developer can build the software that executes the process.

The domain expert knows **what the process should be**.

Toro must therefore solve the more fundamental problem first:

> **How do we convert human expertise into reliable autonomous execution?**

---

# 3. Toro's Initial User Is the Domain Expert

The initial Toro builder should not be assumed to be a software engineer.

The initial builder may be:

* an accountant
* an insurance professional
* a logistics operator
* a procurement specialist
* a payroll professional
* a legal operations specialist
* a customer-support expert
* or any other person with deep knowledge of a valuable business process

Their responsibility is to provide the knowledge.

Toro's responsibility is to provide the machinery that transforms that knowledge into autonomous work.

The expert should not need to understand:

* agent orchestration
* distributed systems
* vector databases
* graph databases
* workflow engines
* message queues
* APIs
* model configuration
* software architecture

They should understand **their profession**.

---

# 4. The Expert Interface

The immediate product priority is therefore an **expert-native interface**.

Toro should not initially ask experts to program workflows manually.

Instead, Toro should allow an expert to teach the system how the work is performed.

For example:

> "When we receive a supplier invoice, what do you check first?"

The expert explains the process.

Toro structures the explanation.

Toro asks about decisions, evidence, exceptions, and escalation.

The resulting knowledge is represented internally as structured domain knowledge and executable workflows.

The expert can then review the result and confirm:

> "Yes. This represents how we actually perform the work."

The complexity of DAGs, agents, permissions, memory, verification, state management, and execution remains underneath the interface.

**The expert describes the work. Toro builds the machinery.**

---

# 5. Domain Graphs

A central output of this process will be the creation of **domain graphs**.

These graphs should represent more than simple workflows.

They should capture:

### Entities

The objects involved in the domain.

### Relationships

How those objects relate to one another.

### Procedures

How work is performed.

### Rules

Professional, organizational, and regulatory rules.

### Evidence

Documents, records, and other information required to make decisions.

### Exceptions

Situations that fall outside the normal process.

### Decision criteria

How an expert determines what should happen.

### Escalation conditions

When autonomous execution should stop and human judgment is required.

### Execution workflows

The actual sequence through which an autonomous worker performs the task.

This domain representation becomes the bridge between **human expertise and autonomous execution**.

---

# 6. Toro as a Knowledge-to-Execution System

The strategic abstraction we are pursuing is therefore not simply:

> AI application builder.

It is:

> **Knowledge → executable autonomous work.**

The intended loop is:

**Domain expert**

↓

**Knowledge capture**

↓

**Domain graph**

↓

**Executable workflow**

↓

**Autonomous agent**

↓

**Verification**

↓

**Real-world execution**

↓

**Telemetry and exceptions**

↓

**Expert correction**

↓

**Improved domain knowledge**

The system should become progressively better as real work exposes cases that were not initially encoded.

This creates an important feedback loop:

> **Human expertise creates the initial system. Real execution teaches the system what the expert did not explicitly encode.**

---

# 7. Deliberately Doing Things That Do Not Scale

The first stage of this strategy will be intentionally manual.

We will:

1. Identify domain experts individually.
2. Speak with them directly.
3. Observe their workflows.
4. Help them encode their knowledge.
5. Guide them through the Toro interface.
6. Observe where they struggle.
7. Record recurring problems.
8. Improve the product.
9. Repeat.

This is not an operational model we intend to maintain indefinitely.

It is a **product discovery strategy**.

The objective is to discover the interface and abstractions that eventually allow the process to become self-service.

We should not prematurely automate a process we do not yet understand.

---

# 8. We Are Not Building a Managed Micro-Founder Program

The concept of "micro-founders" was useful in identifying the opportunity to empower individuals to build businesses on Toro.

However, we should not formalize this as a managed organizational program.

People who build on Toro should simply be considered **independent builders and businesses using the platform**.

They may be:

* domain experts
* developers
* entrepreneurs
* consultants
* teams
* combinations of the above

Toro should not need to manage them individually.

The platform should eventually allow them to build, deploy, operate, and monetize their own applications independently.

They can determine their own:

* product
* customer
* pricing
* distribution
* margins
* business model

Toro provides the underlying infrastructure.

---

# 9. Developers Are Not Being Rejected

Developers remain strategically important.

However, the developer platform should come after we have established the underlying primitives through real domain applications.

The future sequence is:

### Phase 1

**Domain experts → Toro**

### Phase 2

**Domain experts → autonomous applications**

### Phase 3

**Applications → real business activity**

### Phase 4

**Business activity → agent-to-agent interactions**

### Phase 5

**Agent interactions → network**

### Phase 6

**Mature platform → developers**

At that point, developers will have a substantially more powerful platform to build on.

Instead of asking developers to discover what Toro should become, we will provide them with primitives already validated through real economic activity.

---

# 10. Why This Bootstraps the Network Faster

The primary reason for this sequencing is the relationship between applications and the eventual network.

An application by itself is not the objective.

The application creates an autonomous worker.

The worker performs real work.

That work generates real business events.

Those events create relationships with other businesses.

Those relationships create opportunities for agents to interact.

For example:

**Accounting agent**

interacts with

**Supplier agent**

which interacts with

**Logistics agent**

which interacts with

**Retailer agent**

which interacts with

**Bank or payment agent**.

This is how Toro progresses from applications to a network.

The network is therefore bootstrapped from **real domain capabilities**, not from developer adoption alone.

---

# 11. The Role of Accounting

Accounting is the natural first domain because Toro already possesses strong domain expertise and an existing operational foundation.

Accounting provides a useful environment in which to prove the core thesis because it contains:

* structured business entities
* repeatable workflows
* explicit rules
* large volumes of transactional data
* evidence requirements
* reconciliation
* exceptions
* regulatory considerations
* measurable outcomes

The objective is not merely to build better bookkeeping software.

Accounting is the first laboratory for proving that:

> **Toro can take expert knowledge, encode it into a domain graph, convert that graph into autonomous work, and deploy that work in a real business environment.**

Once this is proven, the underlying methodology can be applied to other domains.

---

# 12. Product Priorities

For this phase, Toro should prioritize:

### Core Infrastructure

* agent execution
* persistent state
* ToroDB
* organizational memory
* permissions
* workflow execution
* verification
* communication
* telemetry
* exception handling
* recovery

### Expert Experience

* knowledge capture
* domain modeling
* domain graph construction
* workflow generation
* workflow review
* testing
* correction
* deployment
* monitoring

### Operational Infrastructure

* organization identity
* usage metering
* billing
* auditability
* security
* customer isolation

The developer SDK is deliberately not the primary product priority at this stage.

---

# 13. What We Will Defer

Until the expert workflow has been sufficiently validated, we will not make the following primary objectives:

* public developer SDKs
* large developer documentation programs
* developer marketplaces
* developer certification
* large third-party developer acquisition campaigns
* complex partner programs
* extensive self-service developer infrastructure

These remain future opportunities.

We will build them when the underlying Toro primitives have been proven.

---

# 14. Success Metrics

The central metric for this phase should be:

## Domain-to-Agent Conversion Time

How quickly can we move from:

**Expert knowledge**

to

**validated domain graph**

to

**working autonomous process**

to

**real customer usage**?

Supporting metrics should include:

* domain experts onboarded
* domain graphs created
* workflows created
* successful autonomous executions
* autonomous completion rate
* human escalation rate
* exceptions discovered
* expert corrections captured
* time required to encode a new process
* time required to deploy a new autonomous worker
* real business transactions processed
* business events generated
* agent-to-agent interactions created

These metrics directly measure whether Toro is becoming better at converting expertise into autonomous economic activity.

---

# 15. Board-Level Principle

The Board should use the following principle when evaluating product and engineering priorities during this phase:

> **Optimize for knowledge acquisition before developer acquisition.**

We should prioritize activities that bring valuable domain knowledge into Toro and convert that knowledge into reliable autonomous workers.

The question for every major initiative should be:

> **Does this help us get more real domain expertise into Toro, turn it into autonomous work, and move us closer to agent-to-agent interaction?**

If not, it should be evaluated carefully before receiving significant resources.

---

# Final Decision

Toro will **go after domain experts first**.

We will build the platform layers required for autonomous execution and build an expert-native interface for encoding domain knowledge.

We will initially work with experts individually and manually.

We will deliberately do things that do not scale.

We will use these interactions to discover and refine the scalable product.

We will not abandon developers. We will defer the developer platform until the underlying primitives have been validated through real domain applications.

The strategic sequence is:

> **Expert knowledge → domain graph → autonomous worker → real business activity → agent-to-agent interaction → network.**

The immediate mission is therefore:

> **Find experts. Capture their knowledge. Turn that knowledge into autonomous workers. Put those workers into the real economy. Build the network from there.**
