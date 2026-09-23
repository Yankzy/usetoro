# Internal Memo: Native B2B Products on the Toro Enterprise Graph

## Thesis

Toro should begin with bookkeeping, but bookkeeping is not the end product.

Bookkeeping is the initial system through which businesses bring their financial and operational state onto Toro. As more businesses join, Toro gains something much more valuable than another accounting database: a live graph of companies, suppliers, customers, banks, obligations, transactions, commercial relationships, and eventually agents acting on behalf of each participant.

This creates a new class of products that are native to the network itself.

The long-term strategy is therefore:

**Bookkeeping → Enterprise Graph → Network-native B2B products**

At sufficient network density, bookkeeping can eventually become free or heavily subsidized because its strategic purpose is to pull businesses into the graph. The higher-value products sit on top of that graph.

---

## 1. Business Discovery Network

### What it is

A live B2B discovery layer where businesses can find suppliers, customers, manufacturers, distributors, service providers, logistics partners, and other businesses through agents.

This is not a static directory.

A company should be able to tell its agent:

> Find me three packaging suppliers in Morocco that can handle our expected volume, deliver within our required timeframe, and satisfy our commercial requirements.

Its agent can search the Enterprise Graph, contact candidate agents, request information, verify capabilities, and return qualified options.

### Why it becomes native

Bookkeeping naturally exposes the relationships between companies.

Toro learns that:

* Company A regularly supplies Company B.
* Company C buys a particular category of goods.
* Company D operates in a certain region.
* Company E repeatedly serves businesses with a particular operational profile.

With appropriate permissions, these relationships can help agents discover counterparties far more intelligently than a traditional business directory.

### Long-term evolution

Directory → intelligent matching → agent-to-agent qualification → autonomous commercial discovery.

---

## 2. Commercial Credit Network

### What it is

A network that helps businesses prove that they can fulfill commercial obligations and, when they cannot yet do so confidently, helps them obtain the financing required to make the transaction viable.

This should replace the idea of a universal Toro "risk score."

Businesses should not feel that Toro is secretly ranking them or exposing their internal financial state.

Instead, trust becomes contextual and permissioned.

One agent might ask another:

> Can you demonstrate that you are likely to meet these supplier payment obligations over the next twelve months?

The receiving agent privately evaluates the business state.

If the business can satisfy the obligation, it returns an appropriate proof.

If it cannot, the interesting part begins.

### The financing loop

Toro detects the future constraint before responding externally.

For example:

**Commercial request → forward cash-flow simulation → financing gap detected → financing agent activated → banks and lenders queried → offers compared → terms negotiated → business approves → financing secured → commercial assurance provided**

The system therefore does not merely measure whether a business is creditworthy.

It helps the business **become capable of completing the transaction**.

### Why this is strategically important

Toro sits unusually close to the economic reality behind the loan.

Instead of a business simply asking:

> Give me 700,000 MAD.

Toro may be able to explain, with permission:

> This company has a confirmed supplier obligation in three months, predictable incoming receivables, this historical payment behavior, this expected liquidity gap, and a 700,000 MAD facility would bridge the specific shortfall.

This turns the Enterprise Graph into an automated origination layer for banks and other capital providers.

### Long-term evolution

Commercial proof → liquidity forecasting → financing discovery → automated underwriting support → competitive credit marketplace → Commercial Assurance Network.

Ultimately, Toro should not only answer:

> Can this company fulfill the contract?

It should increasingly answer:

> What must happen to make this viable?

---

## 3. Autonomous Procurement Network

### What it is

A machine-to-machine procurement market.

Instead of employees manually searching suppliers, emailing RFQs, collecting quotations, negotiating terms, comparing documents, and coordinating approvals, a company gives its procurement agent an objective.

For example:

> Acquire 40,000 units of this component, delivered before November 15, within these quality, price, payment, and geographic constraints.

The company's agent finds eligible supplier agents.

The supplier agents respond.

The agents can then negotiate:

* pricing
* quantities
* payment terms
* delivery schedules
* warranties
* service levels
* financing requirements
* guarantees
* contractual conditions

Humans intervene where judgment or approval is required.

### Why it becomes native

The Business Discovery Network provides counterparties.

The Commercial Credit Network provides confidence that the counterparties can fulfill obligations.

The Enterprise Graph provides prior relationship and transaction context.

Procurement therefore becomes much more than a marketplace.

It becomes an executable negotiation network.

### Long-term evolution

Supplier discovery → RFQ automation → agent negotiation → automated contracting → continuous procurement optimization.

---

## 4. Shared Commercial Relationship Layer

### What it is

A common machine-readable state between companies doing business with one another.

Today, two companies involved in the same transaction maintain separate realities.

The supplier has:

* its invoice
* its receivable
* its delivery record
* its accounting entry

The customer has:

* another copy of the invoice
* its payable
* its receiving record
* its accounting entry

Differences between those realities produce enormous reconciliation work.

Toro should progressively turn commercial relationships into shared economic objects.

An invoice becomes not merely a PDF sent from one company to another, but a stateful object witnessed by the agents representing both sides.

The same applies to:

* purchase orders
* invoices
* deliveries
* payment commitments
* disputes
* credits
* returns
* contractual obligations
* settlements

### Why this matters

If both parties participate in the same verified event, much of today's reconciliation work disappears.

Instead of:

**Company A records event → Company B independently records event → humans reconcile differences**

Toro moves toward:

**Economic event → both companies observe the same event → each company's accounting state updates accordingly**

This is one of the places where the Enterprise Graph becomes fundamentally different from traditional enterprise software.

### Long-term evolution

Document exchange → shared transaction objects → shared commercial state → continuously synchronized intercompany relationships.

---

## 5. Network Working-Capital Engine

### What it is

A system that reasons about liquidity across the Enterprise Graph rather than looking at every company and payment independently.

Once Toro understands receivables, payables, payment schedules, financing facilities, supplier obligations, and expected cash flows across many connected businesses, it can identify coordination opportunities that are invisible when every company operates in isolation.

For example:

* A owes B.
* B owes C.
* C owes D.
* D owes A.

Today, each company separately worries about liquidity and separately attempts to finance itself.

Toro can potentially reason across those obligations.

It could optimize:

* payment timing
* early-payment incentives
* supplier financing
* invoice financing
* obligation netting
* short-term liquidity
* working-capital facilities
* cash buffers
* payment sequencing

### Relationship to the Commercial Credit Network

The Commercial Credit Network solves financing constraints around individual commercial relationships.

The Working-Capital Engine goes one level higher.

It looks at the graph itself and asks:

> Is there a better configuration of capital across this network?

Eventually the system may coordinate banks, businesses, suppliers, and other capital providers to keep commercial relationships functioning with dramatically less friction.

### Long-term evolution

Cash forecasting → payment optimization → financing coordination → graph-wide liquidity optimization.

---

# How the Layers Reinforce Each Other

These products should emerge in roughly this structural order:

**Bookkeeping**

creates the underlying economic state.

**Business Discovery**

uses the graph to connect businesses.

**Commercial Credit**

allows those businesses to establish whether transactions are economically supportable and helps remove financing constraints.

**Autonomous Procurement**

lets their agents discover, evaluate, negotiate, and form commercial relationships.

**Shared Commercial State**

makes those relationships machine-native rather than document-native.

**Network Working Capital**

optimizes the flow of capital across the resulting economic network.

The crucial point is that each layer makes the next layer possible.

Toro therefore does not need to predict every application that will eventually exist.

The primary objective is to create enough economic density that new products become obvious consequences of the graph.

---

# Strategic End State

The long-term product is not bookkeeping software.

It is not procurement software.

It is not a lending marketplace.

It is not a B2B directory.

Those become interfaces into a larger system.

The underlying asset is a **network of enterprise agents operating over a shared economic graph**.

Businesses join initially because Toro performs work for them.

Their relationships bring other businesses into the system.

Those relationships generate structured economic state.

That state makes discovery, credit, procurement, financing, and coordination increasingly powerful.

Eventually, bookkeeping itself can become a subsidized acquisition mechanism:

**Free bookkeeping → more businesses → more relationships → denser graph → better network products → more economic activity occurs through Toro**

At sufficient density, the competitive advantage is no longer merely software quality.

It is that a meaningful portion of the economy is already connected to the network.
