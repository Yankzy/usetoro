# Futuro

## Internal Product Memo

### Summary

Futuro is Toro’s composable expert intelligence network.

Its purpose is to help qualified professionals make money from their expertise by turning professional judgment into signed, executable artifacts that can be composed with other expert artifacts to produce complete business outcomes.

Futuro is not a visible marketplace.

Businesses do not browse catalogs of consultants, accountants, tax specialists, controllers, auditors, insurers, lawyers, engineers, or artifacts.

They tell Toro what outcome they need.

Toro determines:

* what professional capabilities are required
* which existing company state can be reused
* which expert artifacts are needed
* in what order those artifacts must execute
* which steps can run in parallel
* what each step will cost
* which expert is responsible for each capability
* what data each artifact will consume
* what output each artifact will produce
* what the final composed outcome will be

Toro then presents that chain transparently to the business before execution.

The business understands what is going to happen, who is contributing, what each step costs, and how the final result will be constructed.

The central product thesis is:

> **One expert creates intelligence. Toro composes intelligence into outcomes.**

The central infrastructure thesis is:

> **Telemetry allows Toro to discover expertise, identify repeatable professional behavior, create new artifacts, understand how artifacts compose, and continuously manufacture new professional products.**

The central economic thesis is:

> **Experts should be able to turn professional knowledge into recurring income-producing assets, while businesses receive sophisticated multi-expert outcomes at dramatically lower coordination and execution cost.**

---

# 1. Core Thesis

Professional services firms exist partly because complicated outcomes require multiple kinds of expertise.

A business may need:

* bookkeeping
* accounting review
* controllership
* tax analysis
* audit preparation
* treasury advice
* insurance analysis
* legal review
* financial planning
* management advisory

Today, obtaining a complete outcome usually requires an organization to coordinate these people manually.

The professional-services firm acts as the glue.

It provides:

* staffing
* coordination
* document collection
* knowledge transfer
* project management
* review
* billing
* reputation
* accountability

Futuro replaces much of that organizational glue with software.

Independent professional expertise becomes executable and composable.

```text
Expert A
   ↓
Artifact A

Expert B
   ↓
Artifact B

Expert C
   ↓
Artifact C

        ↓

Toro Composition Engine

        ↓

Complete business outcome
```

The experts do not need to belong to the same firm.

They do not even need to know one another.

Their artifacts need to speak a common language.

That language is Toro state.

---

# 2. Telemetry Is the Foundation

Futuro cannot exist without telemetry.

Toro must understand not only the final outputs of professional work, but how work is actually performed.

Telemetry should capture enough structured information to answer questions such as:

* What did the expert inspect?
* What information did they request?
* Which evidence mattered?
* What calculations did they perform?
* Which tools did they use?
* Which intermediate conclusions did they reach?
* Which decisions did they make?
* What conditions caused escalation?
* What actions followed?
* Which outputs were consumed by another professional?
* Which sequences of work repeatedly occur together?
* Which business questions repeatedly lead to the same professional process?
* Which expert repeatedly performs a valuable methodology?
* Which capability is currently missing from the network?

Telemetry therefore becomes the raw material from which Futuro discovers products.

Conceptually:

```text
Professional activity
        ↓
Telemetry
        ↓
Pattern detection
        ↓
Reusable methodology
        ↓
Candidate artifact
        ↓
Verified expert artifact
```

Without telemetry, Toro would have to ask experts to manually design products.

With telemetry, Toro can observe professional work, infer reusable structures, and propose the product itself.

---

# 3. Telemetry Creates the Expert Graph

Toro should gradually build an internal graph of professional capability.

Telemetry helps Toro understand:

```text
Expert
  ↓
Performs capability
  ↓
Consumes state
  ↓
Uses evidence
  ↓
Produces output
  ↓
Output consumed by another capability
```

Repeated observations create stronger evidence about expertise.

For example:

```text
Expert 182

Repeatedly performs:
Restaurant working-capital analysis

Consumes:
AccountingState
AccountsReceivable
AccountsPayable
PayrollState

Produces:
LiquidityAssessment
CashActionPlan

Observed:
31 executions
```

This does not automatically authorize publication.

Professional qualifications still need independent verification.

But telemetry tells Toro:

> This person appears to possess a repeatable capability worth productizing.

That becomes an artifact opportunity.

---

# 4. Telemetry Creates New Products

Futuro should actively search telemetry for monetizable patterns.

There are at least four major discovery mechanisms.

## Supply-Driven Discovery

Toro notices that an expert repeatedly performs the same valuable work.

```text
Repeated expert behavior
        ↓
Pattern detected
        ↓
Candidate methodology
        ↓
Artifact proposed
```

Toro might tell the expert:

> You have performed this type of analysis many times. We believe your methodology can become a paid Futuro artifact. We have prepared a draft for you to review.

---

## Demand-Driven Discovery

A business requests an outcome for which no suitable artifact exists.

```text
Business request
        ↓
Capability gap
        ↓
Relevant experts identified
        ↓
Prior professional telemetry analyzed
        ↓
Candidate artifact generated
```

Toro can then approach qualified experts whose prior work suggests they can provide that capability.

---

## Composition-Driven Discovery

Toro observes that several artifacts are repeatedly used together.

For example:

```text
Accounting Review
        +
Controller Review
        +
Cash-Flow Forecast
```

This repeated chain may become a new composed product:

> SME Financial Health Review

Toro can package the sequence without destroying attribution or the economics of the underlying artifacts.

---

## State-Gap Discovery

Toro may discover that a useful state exists but no professional capability currently consumes it.

For example:

```text
ControlledAccountingState
        ↓
????
        ↓
ManagementDecision
```

That missing transition is itself a product opportunity.

Toro can look for experts capable of filling it.

---

# 5. Composability Is the Foundation of the Artifact Model

No Futuro artifact should be designed as an isolated product.

Every artifact must be designed to participate in a larger capability graph.

An artifact should be able to:

* consume Toro company state
* consume outputs from prior artifacts
* generate typed outputs
* emit evidence and provenance
* expose limitations
* declare preconditions
* declare required credentials
* declare jurisdiction
* declare permissions
* declare pricing
* expose escalation conditions
* propose or execute authorized actions
* be invoked by another artifact
* be invoked by an agent

Conceptually:

```text
Artifact<InputState>
        ↓
OutputState
```

Or more generally:

```text
Artifact<
    CompanyState,
    Evidence,
    PriorArtifactOutputs
>
        ↓
Decision
+ Evidence
+ Recommendations
+ Optional State Transition
```

This contract is what makes large professional outcomes composable.

---

# 6. Bookkeeping Is the First State-Producing Layer

Toro bookkeeping should not be treated as the final product.

It is the first major source of dependable company state.

Once bookkeeping becomes production-ready, Futuro should launch immediately around every high-value professional capability that can consume bookkeeping or accounting state.

Initial progression:

```text
Bookkeeping
      ↓
Accounting Review
      ↓
Controllership
      ↓
Tax
      ↓
Audit Preparation
      ↓
Management Advisory
```

Parallel branches can emerge immediately:

```text
Accounting State
      ├── Treasury
      ├── Working Capital
      ├── Insurance
      ├── Lending
      ├── Compliance
      ├── Valuation
      ├── Due Diligence
      └── CFO Advisory
```

The launch objective should therefore be:

> **Turn bookkeeping into the substrate for an expanding graph of professional intelligence.**

---

# 7. Example: Restaurant Management Advisory

Consider a restaurant owner telling their Toro agent:

> I need management advice. I want someone to look at the business and tell me what I should be doing differently.

The user should not need to know which professionals are required.

Toro interprets the desired outcome:

```text
Desired outcome:
Restaurant Management Advisory
```

Toro then works backward through the capability graph.

Management advisory may require:

```text
Management Advisory
        ↑
Controller Assessment
        ↑
Accounting Review
        ↑
Bookkeeping State
```

It may also require parallel inputs:

```text
Cash-Flow Analysis
Margin Analysis
Tax Position
Working-Capital Assessment
```

Toro creates an execution plan.

For example:

```text
Restaurant Management Advisory

1. Existing bookkeeping state
        ↓
2. Accounting review
   Expert: Verified Accountant
        ↓
3. Controller review
   Expert: Verified Controller
        ↓
4. Working-capital analysis
   Expert: Finance Specialist
        ↓
5. Management advisory
   Expert: Restaurant Advisor
        ↓
Final management report
```

This is the core Futuro experience.

---

# 8. Composition Must Be Transparent

The composed chain should never be opaque.

Before a business pays or authorizes execution, Toro should explain:

* the outcome requested
* the proposed expert chain
* which existing company state will be reused
* each professional capability involved
* who authored each artifact
* why each capability is necessary
* what each artifact consumes
* what each artifact produces
* how outputs flow into downstream artifacts
* which actions require approval
* how long-lived the resulting outputs are
* the total price
* the price contribution of each component where appropriate

For example:

```text
Restaurant Management Advisory

Existing Toro bookkeeping state
No additional charge

Step 1
Accounting Review
Expert: Amina B.
Price: 120 MAD

Step 2
Controller Assessment
Expert: Karim R.
Consumes:
Accounting Review
Price: 180 MAD

Step 3
Working-Capital Review
Expert: Sara T.
Consumes:
Controlled Accounting State
Price: 110 MAD

Step 4
Restaurant Management Advisory
Expert: Omar K.
Consumes:
Controller Assessment
Working-Capital Review
Business operating state
Price: 240 MAD

Total:
650 MAD
```

Toro then asks:

> Do you want to start?

Only after authorization does execution begin.

---

# 9. Transparency Is Part of Trust

AI should reduce friction without hiding the professional process.

Futuro should make professional work easier to understand than traditional consulting.

A business should be able to see:

```text
Where did this conclusion come from?

Which expert methodology produced it?

What evidence was used?

Which earlier artifact supplied this input?

What assumptions were made?

What did each step cost?

What action will happen next?
```

Every final outcome should therefore contain a traceable execution graph.

Conceptually:

```text
Final Advisory
    │
    ├── Controller Artifact v3.1
    │      └── Accounting Artifact v7.2
    │             └── Toro Bookkeeping State
    │
    ├── Working Capital Artifact v2.4
    │
    └── Restaurant Advisory Artifact v5.0
```

The user should be able to inspect this graph.

---

# 10. The Artifact Contract

Every artifact must expose a strict machine-readable contract.

At minimum:

```text
artifact_id

version

author

verified_credentials

jurisdiction

capability

inputs

accepted_input_types

required_company_state

required_evidence

preconditions

outputs

output_types

permissions

tools

models

policies

limitations

confidence_rules

escalation_rules

pricing

signature

execution_trace

composition_rules
```

Typed inputs and outputs are fundamental.

Example:

```text
Artifact A

output:
ControlledAccountingState@v3
```

and:

```text
Artifact B

input:
ControlledAccountingState >= v2
```

Toro can therefore determine automatically whether two artifacts are compatible.

---

# 11. The Capability Graph

Futuro should maintain a graph of all professional capabilities.

Nodes may represent:

* company state types
* artifacts
* professional capabilities
* experts
* evidence types
* actions
* outputs

Edges may represent:

* consumes
* produces
* requires
* verifies
* depends on
* can follow
* can substitute
* can compose with
* authored by
* approved by

Example:

```text
BookkeepingState
      ↓
AccountingReview
      ↓
ControlledAccountingState
      ↓
ControllerReview
      ↓
ManagementFinancialState
      ↓
ManagementAdvisory
```

Parallel:

```text
ControlledAccountingState
      ├── TaxAnalysis
      ├── InsuranceRisk
      ├── LendingAssessment
      └── TreasuryAnalysis
```

This graph becomes Toro's map of professional work.

---

# 12. Chain Identification

When a user requests an outcome, Toro must solve a planning problem.

The system works backward from the desired result.

Example:

```text
Desired:
ManagementAdvisory
```

Toro determines:

```text
ManagementAdvisory requires:
ManagementFinancialState

ManagementFinancialState requires:
ControllerAssessment

ControllerAssessment requires:
ControlledAccountingState

ControlledAccountingState requires:
AccountingReview

AccountingReview requires:
BookkeepingState
```

Toro can then inspect what state already exists.

If bookkeeping is already complete:

```text
BookkeepingState:
AVAILABLE
```

that portion of the chain does not need to be repeated.

This creates a minimal execution graph.

Toro should therefore optimize for:

* correctness
* evidence quality
* professional qualification
* compatibility
* cost
* reuse of existing state
* minimal redundant work
* latency
* user preferences
* jurisdiction

The Composition Engine is effectively solving:

> **What is the safest and most efficient chain of verified capabilities that transforms the state we currently have into the outcome the user wants?**

---

# 13. Toro Builds Dynamic Expert Teams

A user should not need to assemble their own professional team.

They specify an outcome.

Toro creates the team.

For example:

> I want to acquire this company.

Toro might determine that the complete chain requires:

```text
Bookkeeping normalization
        +
Accounting review
        +
Financial due diligence
        +
Tax due diligence
        +
Legal review
        +
Insurance review
        +
Operational assessment
        +
Valuation
        ↓
Acquisition Assessment
```

Each capability may belong to a different professional.

Toro makes them function like one coordinated firm.

---

# 14. Toro Replaces Coordination Cost

A large portion of traditional professional-services cost is not pure expertise.

It comes from:

* collecting documents
* moving information between people
* data entry
* reconciling incompatible systems
* preparing workpapers
* scheduling
* explaining previous work
* duplicate analysis
* preparing context for the next professional
* project management
* administrative overhead

Toro already owns much of the underlying company state.

Artifacts exchange structured state directly.

Therefore:

```text
Traditional process

Expert A
  ↓
Document
  ↓
Email
  ↓
Meeting
  ↓
Spreadsheet
  ↓
Expert B
```

becomes:

```text
Artifact A
  ↓
Typed verified output
  ↓
Artifact B
```

This is why Futuro can make sophisticated professional chains dramatically cheaper.

The reduction does not primarily come from paying experts less.

It comes from eliminating coordination waste and repeated manual work.

---

# 15. Toro Builds the Artifact for the Expert

Experts should not be required to learn how Futuro works technically.

They should continue doing their normal professional work.

Toro should use telemetry to identify repeatable valuable behavior and then create the artifact on their behalf.

Toro handles:

* knowledge extraction
* methodology reconstruction
* input schemas
* output schemas
* calculations
* tools
* policies
* runtime logic
* evidence requirements
* permissions
* tests
* simulations
* versioning
* composition interfaces
* packaging
* distribution

The expert handles:

* professional judgment
* corrections
* validation
* approval
* authorship

---

# 16. Expert Sign-Off Is Mandatory

No artifact should be attributed to an expert without explicit approval.

Lifecycle:

```text
Telemetry
    ↓
Candidate methodology
    ↓
Toro artifact draft
    ↓
Testing
    ↓
Simulation
    ↓
Expert review
    ↓
Expert corrections
    ↓
Revalidation
    ↓
Signature
    ↓
Publication
```

The expert receives:

* methodology
* parameters
* assumptions
* inputs
* outputs
* sample cases
* limitations
* edge cases
* composition behavior
* pricing proposal
* permissions
* escalation conditions

The expert can modify the artifact conversationally.

For example:

> Do not apply this methodology when the company has less than twelve months of operating history.

Toro changes the artifact, retests it, and asks for a new sign-off.

---

# 17. Signed Professional Intelligence

Approval should be bound to an exact artifact version.

Conceptually:

```text
artifact_id
version
expert_id
credential_scope
artifact_hash
approved_at
signature
```

Any material modification invalidates the previous approval.

Composition does not erase these signatures.

Every artifact inside a composed execution retains its own attribution.

---

# 18. Provenance Must Survive the Entire Chain

If five professional artifacts contribute to one final result, Toro must preserve where every material conclusion originated.

For example:

```text
Recommendation:
Delay expansion by 90 days

Inputs:

Liquidity concern
Source:
Working Capital Artifact
Expert A

Tax exposure
Source:
Tax Artifact
Expert B

Margin deterioration
Source:
Controller Artifact
Expert C

Final synthesis
Source:
Restaurant Advisory Artifact
Expert D
```

This provides both trust and accountability.

---

# 19. The Artifact Is an Executable Runtime

A Futuro artifact is not a static report.

It is an executable runtime.

It may:

* retrieve company state
* consume upstream artifact outputs
* request missing information
* calculate
* simulate
* reason
* call deterministic tools
* invoke models
* invoke other artifacts
* produce structured outputs
* explain conclusions
* escalate
* request approval
* execute authorized actions
* monitor future conditions

The agent provides the conversational interface to this runtime.

---

# 20. Experts Remain Visible

Futuro should never make experts invisible.

Artifacts should prominently identify their creators.

Professional identity may include:

* name
* photograph where appropriate
* profession
* verified credentials
* specialization
* jurisdiction
* experience
* artifact portfolio
* businesses served
* direct contact through Toro
* phone number where appropriate

Futuro should create both:

* income
* professional exposure

for participating experts.

---

# 21. Expert Monetization

The central expert proposition is:

> **Toro turns professional expertise into recurring income-producing assets.**

Instead of:

```text
Time
 ↓
Client
 ↓
Payment
```

Futuro creates:

```text
Expertise
   ↓
Verified artifact
   ↓
Repeated execution
   ↓
Recurring revenue
```

Composability expands this.

An expert may earn even when their artifact is only one component inside a larger professional service.

---

# 22. Experts Earn From Composition

A user may pay one price for a complete outcome.

Toro then allocates economics across contributing capabilities.

Example:

```text
Restaurant Management Advisory

Customer price:
650 MAD

Accounting Review:
120 MAD

Controller Assessment:
180 MAD

Working-Capital Analysis:
110 MAD

Restaurant Advisory:
140 MAD

Toro infrastructure/composition:
100 MAD
```

The exact economic model can evolve.

The principle should remain:

> **Every expert artifact that contributes meaningful value should participate economically in the outcome.**

---

# 23. Expert Portfolios

Over time, experts accumulate portfolios of professional assets.

Example:

```text
Restaurant Cash Guard
Supplier Payment Policy
Restaurant Expansion Review
Working Capital Assessment
Monthly CFO Review
```

An expert dashboard might show:

```text
Active artifacts:
14

Direct invocations:
1,400

Composed invocations:
6,980

Businesses served:
3,420

Monthly artifact income:
38,500 MAD

Human engagements generated:
17

New artifact opportunities:
3
```

This is a major source of platform stickiness.

---

# 24. Futuro Should Create Economic Stickiness

Traditional software creates stickiness because switching is inconvenient.

Futuro can create stickiness because leaving has an economic cost.

An expert may eventually think:

> Toro distributes my expertise.

> Toro sends me clients.

> Toro monetizes knowledge I already have.

> Toro discovers new products I can sell.

> Toro runs my artifacts.

> Toro builds my professional reputation.

> Toro creates recurring income for me.

That relationship is much deeper than software usage.

Toro becomes part of the professional's income infrastructure.

---

# 25. Human Escalation Remains a Capability

Automation should not pretend that every professional situation can be resolved autonomously.

Artifacts should explicitly encode escalation conditions.

For example:

```text
Artifact conclusion:

Automated assessment completed.

Professional judgment required:
YES

Reason:
Tax treatment depends on unresolved legal classification.
```

Toro can then connect the business with the expert.

Artifacts therefore generate two kinds of income:

```text
Automated artifact income
        +
Human professional engagements
```

---

# 26. Cross-Vertical Composition

Futuro becomes increasingly valuable as the capability graph crosses professional boundaries.

Example:

## Expansion Decision

```text
Accounting Review
      ↓
Controller Review
      ↓
Cash-Flow Modeling
      ↓
Tax Analysis
      ↓
Insurance Analysis
      ↓
Lease Review
      ↓
Financing Assessment
      ↓
Management Advisory
```

Another example:

## Acquisition Assessment

```text
Financial Due Diligence
         +
Tax
         +
Legal
         +
Insurance
         +
Operations
         +
Valuation
         ↓
Acquisition Assessment
```

Toro does not need one firm to own all capabilities.

It composes them.

---

# 27. The Composition Engine

The Composition Engine is a central Futuro infrastructure component.

Given a desired outcome, it must determine:

1. What outcome is requested?
2. What company state already exists?
3. What intermediate state is required?
4. Which capabilities can generate those states?
5. Which experts are qualified?
6. Which artifact versions are compatible?
7. Which artifacts must run sequentially?
8. Which can run in parallel?
9. What evidence is missing?
10. What permissions are required?
11. What is the total price?
12. How should economics be distributed?
13. Where is human approval required?
14. Where is professional escalation required?

The result is an artifact DAG.

```text
User objective
      ↓
Capability planning
      ↓
Transparent artifact DAG
      ↓
User authorization
      ↓
Execution
      ↓
Verified outcome
```

This fits naturally with Toro's broader graph-based execution architecture.

---

# 28. Transparent Pricing

Pricing should happen before execution whenever possible.

Toro should present the composed service as one understandable package while also exposing its components.

For example:

> You asked for a full management advisory review.

> Toro recommends a four-stage professional chain.

> Your bookkeeping is already complete, so it does not need to be repeated.

> The remaining chain will cost 650 MAD.

> Would you like to begin?

The user may inspect the breakdown before approving.

Transparency should be a competitive advantage.

---

# 29. Reuse of Existing State

One of Futuro's largest cost advantages comes from never repeating work unnecessarily.

If Toro already has:

```text
VerifiedBookkeepingState
```

the accountant should not rebuild it.

If Toro already has:

```text
ControlledAccountingState
```

the tax artifact can consume it directly.

If the management advisory requires a controller output that is only one week old and still valid, Toro may reuse it rather than rerun the entire chain.

Every state object should therefore include:

* version
* provenance
* validity period
* evidence
* authoring artifact
* execution date
* confidence
* invalidation rules

This makes professional state reusable infrastructure.

---

# 30. Quality and Trust

Futuro must not become an unverified marketplace.

Toro controls:

* expert verification
* scope of expertise
* artifact publication
* tests
* compatibility
* permissions
* provenance
* signatures
* monitoring
* suspension
* revalidation

Qualification should be granular.

Example:

```text
Verified:

Moroccan SME accounting
Hospitality accounting
Working-capital advisory

Not verified:

International tax
Insurance underwriting
Statutory audit opinions
```

Artifacts cannot silently extend beyond the expert's verified scope.

---

# 31. Core Futuro Primitives

Futuro should be built from reusable primitives.

## Telemetry

Captures professional work and system execution.

## Expert Identity

Represents who the professional is.

## Credential

Represents verified qualification and permitted scope.

## Expert Graph

Represents demonstrated professional capability.

## Company State

Represents normalized enterprise truth.

## Evidence

Represents provenance.

## Artifact

Represents executable professional intelligence.

## Capability

Represents what an artifact can accomplish.

## Policy

Represents professional decision logic.

## Tool

Represents deterministic capabilities.

## Model

Represents probabilistic reasoning.

## Runtime

Executes the artifact.

## Agent

Provides interaction.

## Permission

Controls access and actions.

## Signature

Binds an expert to an exact artifact version.

## Trace

Records execution.

## Action

Represents an intended operation.

## Approval

Represents authorization.

## State Transition

Represents a verified result.

## Capability Registry

Stores callable professional capabilities.

## Composition Engine

Builds compatible artifact chains.

## Pricing Engine

Calculates composed execution economics.

---

# 32. Continuous Capability Creation

Futuro should continuously search for new products.

The system should detect:

* recurring expert work
* unmet business demand
* repeated artifact chains
* missing state transitions
* expensive human coordination
* repeated human escalations
* outputs repeatedly consumed by another profession
* high-value work currently performed manually
* emerging demand by industry

Conceptually:

```text
Telemetry
    +
Demand
    +
Existing state graph
    +
Expert graph
        ↓
New artifact opportunities
```

Product creation becomes an ongoing system behavior rather than an occasional manual strategy process.

---

# 33. Distribution

Futuro distribution should be proactive.

Toro may surface professional capabilities through:

* agent conversations
* email
* notifications
* dashboards
* scheduled reports
* contextual recommendations
* expert spotlights

Example:

> Based on your recent financial state, a working-capital review may be useful. Toro has a verified methodology from a hospitality accountant. It would cost 90 MAD to run. Would you like me to apply it?

The expert receives exposure without needing a visible marketplace.

---

# 34. Strategic Flywheel

Futuro creates several reinforcing loops.

## Business Demand Loop

```text
More businesses
      ↓
More professional questions
      ↓
More capability gaps
      ↓
More artifacts
      ↓
More complete outcomes
      ↓
More business value
      ↓
More businesses
```

## Expert Income Loop

```text
More expert activity
      ↓
More telemetry
      ↓
More artifact opportunities
      ↓
More expert income
      ↓
More expert participation
      ↓
More expert activity
```

## Composition Loop

```text
More artifacts
      ↓
More possible combinations
      ↓
More complete services
      ↓
More artifact executions
      ↓
More expert income
      ↓
More artifacts
```

## Data Loop

```text
More execution
      ↓
More structured state
      ↓
Better composition
      ↓
More useful outcomes
      ↓
More execution
```

---

# 35. Relationship to A2A

Futuro should naturally evolve toward Toro's agent-to-agent economy.

Initially:

```text
Human
  ↓
Toro Agent
  ↓
Expert Artifact
```

Then:

```text
Human
  ↓
Toro Agent
  ↓
Artifact Graph
  ↓
Outcome
```

Eventually:

```text
Business Agent
      ↓
Capability discovery
      ↓
Artifact composition
      ↓
Expert Agent
      ↓
Other specialized agents
      ↓
Verified state transition
```

At that stage, professional intelligence becomes:

* machine-discoverable
* machine-callable
* machine-composable
* economically transactable

The A2A network can purchase professional capability dynamically.

---

# 36. Long-Term Position

Foundation models provide general intelligence.

Experts provide specialized judgment.

Toro provides:

* telemetry
* company state
* identity
* credentials
* trust
* evidence
* permissions
* execution
* composition
* pricing
* payments
* provenance
* reputation
* coordination

Futuro joins these layers.

```text
Professional intelligence
        +
Professional intelligence
        +
Professional intelligence
        ↓
Toro Composition Engine
        +
Company State
        +
Telemetry
        +
Execution Infrastructure
        ↓
Complete professional outcome
```

The strategic asset is not merely a library of artifacts.

It is the graph connecting:

* businesses
* experts
* qualifications
* demonstrated behavior
* methodologies
* artifacts
* state
* evidence
* demand
* pricing
* execution
* reputation
* economics

---

# 37. Core Product Principles

Futuro should be governed by five principles.

### 1. Monetize expertise

> Experts should be able to turn professional knowledge into recurring income-producing assets.

### 2. Toro builds the product

> Experts should not have to become software builders. Toro should identify product opportunities and construct artifacts for them.

### 3. Everything is composable

> No artifact should be designed as an island. Artifacts should participate in larger professional outcomes.

### 4. Composition must be transparent

> Businesses should understand the expert chain, data flow, pricing, provenance, and execution path before they authorize work.

### 5. Telemetry drives creation

> Toro should continuously use professional and execution telemetry to discover expertise, build artifacts, identify capability gaps, and create new products.

---

# 38. The Goal

Futuro succeeds when a professional can say:

> **I keep doing the work I am good at. Toro recognizes which parts of my expertise can become products, builds those products for me, gives me exposure, and pays me whenever my knowledge creates value, even when my artifact is only one part of a much larger service.**

Futuro succeeds when a business can say:

> **I tell Toro the outcome I need. Toro shows me the professional chain required, the experts involved, what each step will do, what it will cost, and how the pieces fit together. I approve it once, and Toro coordinates the entire process.**

And Futuro succeeds strategically when Toro can say:

> **Our telemetry continuously discovers professional knowledge, our artifact system turns that knowledge into executable capabilities, and our composition engine assembles those capabilities into complete business outcomes.**

The long-term transformation is:

```text
Professional services firm
        ↓
Professional capability graph
        ↓
Composable expert intelligence
        ↓
Transparent autonomous execution
        ↓
Agent-to-agent professional economy
```

That is Futuro.
