This is exactly what takes Toro OS from an "automation tool" to an **Enterprise FinTech Platform**. 

Building a payroll primitive is all about managing state. You do not want to build the UI for a contractor to sign an IRS Form 8655 or a W-4—that takes months. Instead, you will use **Gusto Embedded API** combined with **Gusto Flows** (pre-built, white-labeled UI components) mapped directly into your Go orchestrator.

Here is the technical Product Requirements Document (PRD) for building the `GustoPayroll` Primitive in Toro OS.

---

# PRD: Toro OS Embedded Payroll Primitive (via Gusto)

## 1. Product Objective
To provide Toro OS tenants (like SusannaAI) with a "Full-Stack Payroll" node. This node will allow AI agents to collect hours, calculate taxes, remit payments to the IRS, and direct-deposit funds into workers' bank accounts without the tenant ever leaving the white-labeled portal.

## 2. System Architecture
Your Go Orchestrator will act as the middleware between the SusannaAI UI, the AI Agent, and the Gusto API.

* **Toro OS (Go Backend):** Manages the API keys, the state of the payroll run, and webhooks.
* **Gusto API:** Handles the tax math, the money movement, and the IRS compliance.
* **Gusto Flows (UI):** IFrame-able components you embed in the SusannaAI dashboard for complex legal onboarding (linking bank accounts, signing tax forms).



## 3. The 3 Core Workflows

### A. Company Onboarding (KYB & Tax Setup)
Before a contractor can run payroll, they must be created in Gusto and sign the tax forms.
1.  **API Call:** Toro OS sends a `POST /v1/partner_managed_companies` with the contractor's EIN and business name.
2.  **The UI Handoff:** Toro OS generates a Gusto "Flow URL". You embed this in the SusannaAI dashboard. The contractor clicks it, enters their bank details via Plaid, and signs the IRS forms inside the white-labeled Gusto IFrame.

### B. Employee Onboarding
1.  **API Call:** Contractor tells the AI, "I hired Steve." AI triggers `POST /v1/companies/{company_uuid}/employees`.
2.  **The UI Handoff:** Toro OS generates a "W-4 / Direct Deposit" Flow URL and texts it to Steve. Steve fills out his social security number securely on Gusto's servers. (Your database never touches the raw SSN, shielding you from massive liability).

### C. The Payroll Run State Machine
This is the core primitive your Go Orchestrator will run every week.
1.  `Sync Hours:` Pull hours from the AI Calendar.
2.  `Create Payroll:` `PUT /v1/companies/{id}/payrolls/{id}/prepare`
3.  `Calculate & Preview:` Gusto returns the Gross Pay, Taxes, and Net Pay.
4.  `Submit:` Contractor texts "Approve" -> Toro OS fires `PUT /v1/companies/{id}/payrolls/{id}/submit`.

---

## 4. Go Integration Implementation

Here is how you structure this primitive in your Go backend. 

### Step 1: The Gusto Client Struct
First, create a dedicated HTTP client in Go that handles Gusto's Bearer authentication and rate limiting.

```go
package gusto

import (
	"bytes"
	"encoding/json"
	"net/http"
	"time"
)

type Client struct {
	HTTPClient *http.Client
	APIToken   string
	BaseURL    string
}

func NewClient(token string) *Client {
	return &Client{
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
		APIToken:   token,
		BaseURL:    "https://api.gusto-demo.com/v1", // Use production URL for live
	}
}
```

### Step 2: The Payroll Run Primitive (The Go Worker)
When the YAML blueprint triggers a "Run Payroll" action, your Go worker executes this logic.

```go
// 1. Structs for the Gusto API payload
type PayrollSubmission struct {
	EmployeeCompensations []EmployeeComp `json:"employee_compensations"`
}

type EmployeeComp struct {
	EmployeeUUID     string `json:"employee_uuid"`
	FixedCompensaton string `json:"fixed_compensation,omitempty"`
	HourlyComp       string `json:"hourly_compensations,omitempty"`
}

// 2. The Execution Function
func (c *Client) SubmitPayroll(companyUUID string, payrollUUID string, payload PayrollSubmission) error {
	
	// Convert payload to JSON
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	// Build the Gusto Endpoint URL
	url := c.BaseURL + "/companies/" + companyUUID + "/payrolls/" + payrollUUID + "/submit"
	
	req, err := http.NewRequest("PUT", url, bytes.NewBuffer(body))
	if err != nil {
		return err
	}

	// Add Authorization Header
	req.Header.Add("Authorization", "Bearer "+c.APIToken)
	req.Header.Add("Content-Type", "application/json")

	// Execute via HTTP Client
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		// Handle API errors (e.g., Contractor doesn't have enough funds)
		return fmt.Errorf("Gusto API error: status %d", resp.StatusCode)
	}

	return nil
}
```

### Step 3: YAML Blueprint Definition
To make this work in Toro OS, you abstract the Go code into a YAML node that SusannaAI can trigger via the AI agent.

```yaml
nodes:
  - id: prepare_weekly_payroll
    type: integration.gusto
    action: preview_payroll
    inputs:
      company_id: "{{tenant.gusto_uuid}}"
      pay_period: "2026-04-10_2026-04-17"
      hours_data: "{{calendar_agent.weekly_hours}}"
    on_success:
      transition_to: send_sms_approval

  - id: send_sms_approval
    type: communication.sms
    inputs:
      phone: "{{tenant.owner_phone}}"
      message: "Payroll preview ready: {{prepare_weekly_payroll.gross_total}}. Reply YES to run."
    on_reply_yes:
      transition_to: execute_payroll

  - id: execute_payroll
    type: integration.gusto
    action: submit_payroll
    inputs:
      payroll_id: "{{prepare_weekly_payroll.payroll_uuid}}"
```

## 5. Webhooks (Asynchronous State Management)
Payroll takes time to process. Gusto will pull the money from the contractor's bank via ACH, which takes 2 to 4 days to clear. Your Go Orchestrator must listen for webhooks to update the SusannaAI dashboard.

You will expose an endpoint `POST /webhooks/gusto` in your Go router:

* **Listen for `Payroll.Processed`:** Triggers Toro OS to text the owner: *"Payroll funds successfully withdrawn."*
* **Listen for `Payroll.Funds_Failed`:** (NSF - Non-Sufficient Funds). **CRITICAL.** If this webhook fires, Toro OS must immediately trigger a Circuit Breaker, text the owner: *"URGENT: Your bank rejected the payroll transfer,"* and halt the AI from making further financial commitments.

---

### The Genius of this Architecture
By using **Gusto Flows** for the UI and the **API** for the backend logic, you are outsourcing 100% of the UI design, tax liability, and banking regulations to Gusto. 

Toro OS just remains the puppet master—pulling the API strings based on the AI Agent's text message conversations with the contractor.