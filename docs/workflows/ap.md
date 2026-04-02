# AP Workflow – Broken into Agents, Workers & Tools

Here’s the end-to-end **Accounts Payable** process split into 7 collaborating components. It covers the classic AP cycle: receipt → extraction → matching → approval → payment → reconciliation, with AI reasoning where it adds the most value (e.g., complex coding, discrepancy resolution, approval decisions, early payment discounts).

| Step | Trigger Event (JetStream Subject) | Component Type | Name | Responsibility (LLM reasoning?) | Key Tools it can call | Output Event(s) |
|------|-----------------------------------|----------------|------|---------------------------------|-----------------------|-----------------|
| 1    | `vendor.invoice.received` (email, upload, portal, or QB import) | **Worker** | Invoice Intake Worker | Capture raw invoice (OCR if PDF/email), basic validation, attach to event | `extract_invoice_data_ocr`, `save_attachment`, `detect_duplicates` | `ap.bill.data.extracted` |
| 2    | `ap.bill.data.extracted` | **Agent** | Bill Coding & Matching Agent | LLM reviews extracted data, suggests GL coding, performs 2/3-way matching (PO + receipt), flags discrepancies or tax issues | `lookup_purchase_order`, `match_to_receipt`, `suggest_gl_code`, `validate_tax_and_terms`, `calculate_early_discount` | `ap.bill.ready_for_approval` or `ap.bill.exception` |
| 3    | `ap.bill.ready_for_approval` | **Worker** | Approval Router Worker | Simple rule-based routing (amount thresholds, department, vendor) + dynamic escalation | `get_approval_matrix`, `route_to_approver` | `ap.bill.routed_for_approval` |
| 4    | `ap.bill.routed_for_approval` (or timeout) | **Agent** | Approval Decision Agent | LLM evaluates the bill + context (budget, vendor history, urgency, early discount opportunity) and recommends Approve / Reject / More Info | `check_budget_availability`, `get_vendor_history`, `calculate_discount_savings`, `request_more_info` | `ap.bill.approved`, `ap.bill.rejected`, `ap.bill.more_info_needed` |
| 5    | `ap.bill.approved` | **Worker** | Payment Scheduler Worker | Schedules payment based on terms, cash flow rules, or early discount optimization | `schedule_payment`, `optimize_payment_date` | `ap.payment.scheduled` |
| 6    | `ap.payment.scheduled` + due date or manual trigger | **Worker** | Payment Execution Worker | Executes or prepares the payment (ACH, wire, check via QB Bill Pay or bank integration) | `create_payment_in_quickbooks`, `initiate_bank_payment` | `ap.payment.executed` |
| 7    | `ap.payment.executed` or bank confirmation | **Agent** | Payment Reconciliation Agent | LLM reconciles payment against bill(s), handles partials, fees, currency, multi-bill allocations | `apply_payment_to_bill`, `handle_partial_payment`, `record_gl_entry`, `flag_reconciliation_issue` | `ap.bill.closed`, `ap.payment.reconciled` |

**Branching events** (handled naturally via JetStream):
- `ap.bill.exception` → routes to human review or a dedicated Exception Resolution Agent.
- `ap.bill.rejected` → notifies vendor + logs reason.
- Overdue or discount opportunities can trigger a separate **Cash Optimization Agent** (LLM decides whether to take early discount or hold cash).

This workflow integrates cleanly with QuickBooks (bills, vendors, purchase orders, payments, GL entries).

### Detailed Breakdown of Each Component

#### 1. Workers (Go + JetStream – Simple & Fast)
These handle mechanical steps without LLM cost/latency.

Example for **Invoice Intake Worker**:
```go
func (w *InvoiceIntakeWorker) Start() {
    // Subscribe to incoming invoice events (email webhook, file upload, QB import)
    for msg := range consumer.Messages() {
        rawInvoice := extractFromPayload(msg)
        extracted := w.ocrService.Extract(rawInvoice) // or call external OCR if needed
        publishEvent("ap.bill.data.extracted", extracted)
        msg.Ack()
    }
}
```

Similar simple workers for routing and payment execution.

The **Payment Scheduler Worker** can run on a daily cron to scan approved bills and publish scheduling events, optimizing for cash flow or discounts.

#### 2. Agents (LLM + Tools + Reasoning)
Each agent follows the same Go pattern as in the AR design:

```go
type BillCodingMatchingAgent struct {
    llmClient LLMClient
}

func (a *BillCodingMatchingAgent) Handle(event *Event) {
    context := buildBillContext(event.ExtractedData) // include PO, receipt, vendor terms from QB

    tools := []Tool{
        {Name: "lookup_purchase_order", Description: "Find matching PO by vendor and amount"},
        {Name: "match_to_receipt", Description: "..."},
        {Name: "suggest_gl_code", Description: "Propose GL account and class based on description"},
        // ...
    }

    response := a.llmClient.ChatWithTools(context, systemPromptForCoding, tools)

    // Execute tool calls, feed results back, continue reasoning
    for _, call := range response.ToolCalls {
        result := executeTool(call)
        context.AddResult(result)
    }

    final := a.llmClient.ContinueReasoning(context)
    if final.HasIssues {
        publishEvent("ap.bill.exception", final)
    } else {
        publishEvent("ap.bill.ready_for_approval", final)
    }
}
```

**Key System Prompts** (customize per agent):
- **Bill Coding & Matching Agent**: “You are an expert AP accountant. Analyze the bill, match to PO/receipt if available, suggest accurate GL coding, detect anomalies or fraud indicators, and calculate any early payment discounts.”
- **Approval Decision Agent**: “Evaluate this bill for approval. Consider budget, vendor relationship, business value, discount savings, and any risks. Recommend approve/reject or request more information with clear reasoning.”
- **Payment Reconciliation Agent**: “Reconcile the executed payment against the bill(s). Handle partial payments, fees, currency differences, and ensure proper GL impact.”

#### 3. Tools (Go Functions Exposed via Function Calling)
Implement these once and register with your LLM client:

- `extract_invoice_data_ocr(fileURL or base64)` – if you have an OCR service
- `lookup_purchase_order(vendorID, invoiceNumber, amount)`
- `match_to_receipt(billID, poID)`
- `suggest_gl_code(description, amount, vendor)` – can return multiple suggestions with confidence
- `check_budget_availability(department, amount, period)`
- `get_vendor_history(vendorID)` – returns past payment behavior, disputes, etc.
- `create_bill_in_quickbooks(billPayload)`
- `apply_payment_to_bill(paymentID, billID, amount)`
- `initiate_bank_payment(...)` or `create_payment_in_quickbooks`

All tools return structured results (JSON) that the LLM can reason over in the next step.

### Additional Enhancements

- **Exception Handling**: Dedicated worker/agent for exceptions (missing PO, coding mismatch, duplicate detection).
- **Cash Flow Optimization Agent** (triggered daily or on high-value approved bills): LLM analyzes open bills vs. cash forecast and suggests optimal payment timing or early discount captures.
- **Audit & Reporting Worker**: Periodic summarizer that publishes aggregated AP insights (aging, discount capture rate, etc.).
- **Human-in-the-Loop**: For high-value or flagged bills, publish to a review subject that notifies approvers via Slack/email and waits for an `ap.bill.manually_approved` event.
- **Fraud Detection**: Add a lightweight check in the Coding Agent (LLM can flag unusual amounts, new vendors, etc.).

### Implementation Tips & Order

1. Define JetStream subjects consistently (e.g., `ap.*` namespace) and event schemas.
2. Start with Workers (Intake + Payment Execution) — they are the foundation.
3. Build core Tools (QB bill creation, PO lookup, payment apply).
4. Implement Agents starting with **Bill Coding & Matching Agent** (highest value from AI).
5. Add Approval and Reconciliation last.
6. Test end-to-end with sample invoices (PO → receipt → bill → payment).

This AP workflow pairs beautifully with the AR one you already have — together they give you a complete AI-augmented procure-to-pay and order-to-cash cycle.
