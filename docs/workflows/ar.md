# AR Workflow – Broken into Agents, Workers & Tools

I’m breaking the **full end-to-end Accounts Receivable** process into a clean sequence of 6 collaborating pieces. Each step is triggered by an event.

| Step | Trigger Event (JetStream Subject) | Component Type | Name | Responsibility (LLM reasoning?) | Key Tools it can call | Output Event(s) |
|------|-----------------------------------|----------------|------|---------------------------------|-----------------------|-----------------|
| 1    | `sales.order.approved`            | **Worker**     | Invoice Prep Worker | Simple data gathering (no LLM) | QB read order, read customer, read GL | `ar.invoice.data.ready` |
| 2    | `ar.invoice.data.ready`           | **Agent**      | Invoice Generation Agent | LLM decides invoice structure, line items, taxes, custom terms, discounts, currency, etc. | `create_invoice_in_quickbooks`, `validate_tax_rules`, `calculate_totals`, `lookup_customer_terms` | `ar.invoice.created` |
| 3    | `ar.invoice.created`              | **Worker**     | Delivery Worker | Choose channel (email, portal, PDF), personalize subject/body if needed | `send_email`, `upload_to_customer_portal`, `generate_pdf` | `ar.invoice.sent` |
| 4    | `ar.invoice.sent` + scheduled (daily) | **Worker**     | Payment Monitor Worker | Polls QB (or listens to QB webhooks) for status changes; also checks due dates | `list_invoices_by_status`, `get_invoice_payments` | `ar.payment.received`, `ar.invoice.overdue`, `ar.invoice.due_soon` |
| 5    | `ar.payment.received` or `ar.invoice.due_soon` | **Agent**      | Payment Reconciliation Agent | LLM matches partial payments, multiple invoices, bank fees, currency conversion, etc. | `apply_payment_to_invoice`, `create_credit_memo`, `update_gl_entry`, `classify_payment` | `ar.payment.applied`, `ar.invoice.partially_paid` |
| 6    | `ar.invoice.overdue` (after grace period) | **Agent**      | Collections Agent | LLM decides next action based on customer history, amount, past behavior, relationship value | `send_reminder_email`, `escalate_to_collections`, `offer_payment_plan`, `write_off_small_balance`, `notify_account_manager` | `ar.reminder.sent`, `ar.collections.escalated`, `ar.invoice.closed` |

This creates a **linear + branching workflow** that is fully traceable in JetStream (you can see the event stream for any invoice ID).

### Detailed Breakdown of Each Component

#### 1. Workers (Go + JetStream)
Simple, fast, no LLM. Example skeleton:

```go
// InvoicePrepWorker
func (w *InvoicePrepWorker) Start() {
    consumer := jetstream.NewConsumer(stream, "invoice-prep-consumer")
    for msg := range consumer.Messages() {
        orderID := extractOrderID(msg)
        data := gatherInvoiceData(orderID) // QB API calls
        publishEvent("ar.invoice.data.ready", data)
        msg.Ack()
    }
}
```

Same pattern for Delivery Worker and Payment Monitor Worker (the monitor can run a daily cron job that scans open invoices and publishes overdue/due-soon events).

#### 2. Agents (LLM + Function Calling)
Each agent follows the same Go pattern:

```go
type InvoiceGenerationAgent struct {
    llmClient LLMClient // your OpenAI/Anthropic wrapper
}

func (a *InvoiceGenerationAgent) Handle(event *Event) {
    // 1. Load context from event + QuickBooks
    ctx := buildContext(event.InvoiceData)

    // 2. Define tools for this agent
    tools := []Tool{
        {Name: "create_invoice_in_quickbooks", ...},
        {Name: "validate_tax_rules", ...},
        // ...
    }

    // 3. Run LLM with system prompt + tools
    response := a.llmClient.ChatWithTools(ctx, systemPromptForInvoiceGen, tools)

    // 4. Execute any tool calls the LLM made
    for _, call := range response.ToolCalls {
        result := executeTool(call) // your Go function
        ctx.AddToolResult(result)
    }

    // 5. Final reasoning step → decide next action
    finalDecision := a.llmClient.ContinueReasoning(ctx)

    // 6. Publish next event
    publishEvent("ar.invoice.created", finalDecision.InvoiceID)
}
```

**System prompts** (one per agent) are critical:
- Invoice Generation Agent: “You are an expert AR accountant. Generate a complete, audit-ready invoice…”
- Reconciliation Agent: “Match this payment to open invoices. Consider partials, credits, currency…”
- Collections Agent: “Decide collection strategy based on customer risk score, history, amount owed…”

#### 3. Tools (Go functions exposed to LLM)
All tools are plain Go functions. Examples:

- `create_invoice_in_quickbooks(payload InvoicePayload) (InvoiceID, error)`
- `apply_payment_to_invoice(paymentID, invoiceID, amount, date)`
- `send_reminder_email(customerEmail, invoiceID, tone string)` – tone can be “polite”, “firm”, “urgent” decided by Collections Agent
- `lookup_customer_terms(customerID)` – returns credit terms, past disputes, etc.

You implement these once in Go and register them with the LLM client for function calling.

### Additional Nice-to-Haves (Event-Driven)

- **Orchestrator Worker** (optional): Listens to all `ar.*` events and updates a central AR dashboard / audit log.
- **Human-in-the-Loop Agent**: Triggered by `ar.collections.escalated` → sends Slack/Email to accountant with context and waits for approval event.
- **Reporting Worker**: Daily cron that triggers a lightweight “AR Aging Report Agent” that summarizes overdue buckets using LLM for insights.
- **Retry & Dead-letter**: JetStream built-in. Failed events go to DLQ stream for manual replay.

### Implementation Order Recommendation

1. Define all JetStream subjects + schemas (use protobuf or JSON schema).
2. Implement the three Workers first (they are simplest).
3. Build the Tool layer (QB integration + email + internal DB).
4. Implement the three Agents one by one, starting with Invoice Generation Agent.
5. Wire everything with a small test flow (create a sample order → watch the full event chain).
