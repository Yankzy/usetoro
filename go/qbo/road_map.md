

# The AI Junior Accountant: Technical Roadmap Outline


Here are the relevant assets and vocabulary you need to master for your "AI Junior Accountant" in QuickBooks Online (QBO).

---

## 1. Core Transaction Entities (The "Paperwork")

Your AI needs to handle the "In-Box" and "Out-Box" of a business. In QBO, everything is an Entity.

### Accounts Payable (AP) – Managing Expenses

* **`Bill`**: Represents an invoice received from a vendor. Your AI will "read" a PDF and create these.
* **`Vendor`**: Every bill needs a supplier. You’ll need to search/create vendors to avoid duplicates.
* **`BillPayment`**: When the cash actually leaves the bank to pay a `Bill`.
* **`Purchase`**: Used for immediate expenses (Credit Card or Check) that aren't "bills" to be paid later.

### Accounts Receivable (AR) – Managing Income

* **`Invoice`**: The primary document for sales.
* **`Payment`**: Recording when a customer pays that invoice.
* **`Customer`**: The entity being billed.
* **`CreditMemo`**: Essential for the "Junior Accountant" to handle returns or disputes.

---

## 2. Bookkeeping & General Ledger (The "Brains")

This is where the actual "Accounting" happens. Your AI needs to ensure the books balance.

* **`JournalEntry`**: The "Swiss Army Knife." If an AI doesn't know where a specific transaction fits, or needs to do year-end adjustments, it uses this.
* **`Account` (Chart of Accounts)**: This is your AI's map. Every transaction must be coded to an `Account` (e.g., "Office Supplies" or "Legal Fees").
* **`Transfer`**: Moving money between internal accounts (e.g., Savings to Checking).

---

## 3. Advanced Automation Assets

To be a *solid* accountant, your Go backend needs more than just CRUD operations.

* **`Webhooks`**: **Crucial.** You shouldn't poll the API. You need to listen for when a human manually changes something in QBO so your AI can react or adjust its logic.
* **`CDC` (Change Data Capture)**: If webhooks fail, CDC allows you to ask QBO: "What has changed since my last sync?"
* **`Batch`**: QBO allows you to group up to 25 operations in a single HTTP request. For a "Junior Accountant" processing 100 receipts, this is how you stay under rate limits.
* **`Attachable`**: This is how you link the "Proof" (the PDF receipt) to the `Bill` or `Expense`.

---

## 4. Reporting & Validation (The "Review")

A Junior Accountant’s work is useless if they don't check the totals. Your Go backend should pull these to verify its own work:

* **`Report/BalanceSheet`**: To verify assets vs. liabilities.
* **`Report/ProfitAndLoss`**: To verify the AI's categorization of income and expenses.
* **`Report/GeneralLedger`**: The audit trail of every single entry made.

---

### Technical Workflow for your Go Backend

1. **Sync the Chart of Accounts (`Account`)**: Your AI cannot categorize a transaction if it doesn't know the client's specific accounts.
2. **Fetch Lists (`Customer`, `Vendor`, `Item`)**: Store these locally in your DB to provide "fuzzy matching" for your AI models.
3. **The Transaction Loop**:
* **Input**: AI processes a receipt.
* **Lookup**: Find or create the `Vendor`.
* **Create**: POST a `Bill` or `Purchase` entity.
* **Verify**: Run a `Report/GeneralLedger` to ensure the entry landed in the right spot.



## Road Map

### Phase 1: The Foundation (Identity & Persistence)

* **Token Management Service:** Handling OAuth2 lifecycle, encryption at rest, and multi-tenant `RealmID` routing.
* **The Shadow DB (Mirroring):** Designing the Go-structs and database schema to mirror QBO entities for AI context.

### Phase 2: The Sync Engine (Data Freshness)

* **Webhooks Receiver:** Real-time event handling for "New Invoice" or "Vendor Updated."
* **CDC (Change Data Capture) Worker:** The "Gap Filler" that catches missed events via the `cdc` endpoint.
* **Batch Processing Logic:** Optimizing writes to QBO to stay under the 40 batch-requests-per-minute limit.

### Phase 3: The Accounting Intelligence (Business Logic)

* **Chart of Accounts (CoA) Mapper:** Building the bridge between raw bank text and the client’s specific G/L accounts.
* **Entity Resolution Service:** Matching "Staples #452" to the "Staples" Vendor ID.
* **Attachment Service:** Handling the `Attachable` API for receipt/invoice OCR storage.

### Phase 4: Reliability & Compliance

* **Circuit Breakers & Rate Limiters:** Implementing the "Leaky Bucket" strategy for the 500 requests/min throttle.
* **Audit Logging:** Maintaining a "Reasoning Path" (Why did the AI categorize this as 'Travel'?) for the CPA to review.

---

## Deep Dive: Phase 1 — Token Management & Identity

In a Go backend, you aren't just saving a string; you are managing a living credential. If your AI tries to post a `JournalEntry` and the token is expired, the "Junior Accountant" just quit.

### 1. The Token Schema

You must store tokens with their expiration timestamps. I recommend **AES-256-GCM** encryption for the `AccessToken` and `RefreshToken` before they hit your DB.

```go
type QBOToken struct {
    RealmID              string    `gorm:"primaryKey"`
    AccessToken          string    `json:"-"` // Encrypted
    RefreshToken         string    `json:"-"` // Encrypted
    AccessTokenExpiry    time.Time `json:"access_token_expiry"`
    RefreshTokenExpiry   time.Time `json:"refresh_token_expiry"`
    LastSyncTimestamp    time.Time `json:"last_sync"` // Essential for CDC calls
}

```

### 2. The Transparent Refresh Middleware

Don't write "refresh logic" in your business functions. Wrap your QBO Client in a struct that checks the expiry before every call.

```go
func (c *QBOClient) GetValidToken(ctx context.Context, realmID string) (string, error) {
    token, _ := c.db.GetToken(realmID)
    
    // Refresh 5 minutes before actual expiry to be safe
    if time.Now().Add(5 * time.Minute).After(token.AccessTokenExpiry) {
        newToken, err := c.refreshOAuth2Token(token.RefreshToken)
        if err != nil {
            return "", fmt.Errorf("AI Accountant halted: Token Refresh Failed: %w", err)
        }
        c.db.UpdateToken(realmID, newToken)
        return newToken.AccessToken, nil
    }
    return token.AccessToken, nil
}

```

### 3. Identity Scoping

Since your customers are CPAs, one CPA might have 50 `RealmIDs` (clients). Your backend must be **multi-tenant**. Every API request from your frontend must include the `target_realm_id` so your Go service knows which client's books the AI is currently "working on."

A solid roadmap needs to solve for the "State Problem." Since your AI makes decisions based on the current state of the books, if your backend doesn't know a human just deleted a Vendor, the AI will keep trying to post Bills to a ghost ID.

Here is Phase 2: **The Sync Engine**. We will use a "Hybrid Sync" model: **Webhooks** for speed (push) and **CDC** for reliability (pull).

---

## Phase 2: The Sync Engine (Data Freshness)

### 1. The Webhook Receiver (Real-Time Push)

QuickBooks sends a JSON payload to your endpoint whenever an entity changes.

* **Vocabulary:** `intuit-signature` (HMAC-SHA256 header), `Verifier Token` (from Developer Portal).
* **The Go Challenge:** You must respond with a `200 OK` within **3 seconds**, or Intuit will retry (and eventually blacklist you). Do **not** process the data in the request handler. Use a Goroutine or a Queue.

#### Code Sample: Webhook Signature Verification in Go

```go
func WebhookHandler(w http.ResponseWriter, r *http.Request) {
    // 1. Read Raw Body (Required for HMAC)
    body, _ := io.ReadAll(r.Body)
    
    // 2. Verify Signature
    signature := r.Header.Get("intuit-signature")
    if !isValidSignature(body, signature, os.Getenv("QBO_VERIFIER_TOKEN")) {
        w.WriteHeader(http.StatusUnauthorized)
        return
    }

    // 3. Fast Response (Acknowledge immediately)
    w.WriteHeader(http.StatusOK)

    // 4. Asynchronous Processing
    go processWebhookEvent(body) 
}

func isValidSignature(payload []byte, signature, token string) bool {
    h := hmac.New(sha256.New, []byte(token))
    h.Write(payload)
    expected := base64.StdEncoding.EncodeToString(h.Sum(nil))
    return expected == signature
}

```

---

### 2. The CDC Worker (The "Safety Net" Pull)

Webhooks can fail if your server is down. The **CDC (Change Data Capture)** API allows you to ask: *"What changed for these 10 entities since 2:00 PM?"*

* **Limit:** You can look back up to 30 days.
* **Strategy:** Run a background worker in Go every 60 minutes.

#### The CDC Request Pattern

```text
GET /v3/company/<realmId>/cdc?entities=Account,Invoice,Bill,Vendor&changedSince=2024-03-20T10:00:00Z

```

---

### 3. Entity Reconciliation (The "Upsert")

When your Go backend receives a sync event (from Webhooks or CDC), it needs to update the **Shadow DB**.

| Operation | Action in your Go DB |
| --- | --- |
| `Create` | **Insert** if ID doesn't exist. |
| `Update` | **Update** fields + increment `SyncToken`. |
| `Delete` | **Soft Delete** (mark as `deleted_at`). Don't hard delete; the AI might need the history to understand past patterns. |
| `Merge` | **Update** reference; Point the "Old ID" to the "New ID." (Common when a CPA merges two duplicate vendors). |

---

### 4. Batch Processing (The Efficiency Play)

If your AI processes 20 receipts at once, don't send 20 HTTP requests. Use the **Batch** endpoint.

* You can wrap multiple `Bill` creations into one JSON body.
* This protects your AI from being throttled by the QBO 10-concurrent-requests limit.

### Key Logic Flow for "AI Junior Accountant" Sync:

1. **Bank Feed Input:** AI sees a transaction.
2. **Local Check:** AI queries your Go DB: "Who is the Vendor for 'Amzn Mktp'?"
3. **Conflict Check:** Your DB says "Vendor ID 45."
4. **Verification:** Before posting, the AI checks the `SyncToken` in your DB vs. what it's about to send.


To build an "AI Junior Accountant," Phase 3 is where the magic happens. You aren't just moving data; you are interpreting it. A human junior accountant looks at a receipt from "Shell" and knows it’s "Fuel Expense," but they also know to check if there's a "Fuel" account in that specific client's Chart of Accounts (CoA).

Here is the technical breakdown of Phase 3.

---

## Phase 3: The Accounting Intelligence (Business Logic)

### 1. The CoA Mapper (The AI's Navigation System)

Every QBO company has a unique **Chart of Accounts**. Your AI cannot use a "generic" list of categories; it must map to the specific `Id` and `AccountType` of the client.

**Vocabulary:** * **`AccountType`**: High-level category (e.g., `Expense`, `Revenue`, `Asset`).

* **`AccountSubType`**: Specific detail (e.g., `OfficeGeneralExpenses`, `AdvertisingPromotional`).
* **`Classification`**: Is it a `Balance Sheet` or `Income Statement` account?

**The Logic:** Your Go service should pull the CoA and index it into a vector store or a weighted search table so the AI can "match" raw transaction text to the closest valid QBO Account ID.

---

### 2. Entity Resolution Service (The "Vendor Matcher")

AI often struggles with messy strings like "SQ * PHO BAYSIDE." Your backend needs an **Entity Resolution** layer to prevent duplicate Vendors.

* **Step A:** Search local DB for exact string match.
* **Step B:** If no match, use a Fuzzy Match (Levenshtein distance) or LLM to check if "PHO BAYSIDE" is actually the existing Vendor "Bayside Vietnamese."
* **Step C:** If still no match, create a new `Vendor` entity via the API.

---

### 3. The Transaction Creator (Writing to the Books)

When the AI decides to record an expense, you must choose between a `Bill`, a `Purchase`, or a `JournalEntry`.

* **`Purchase`**: Use this for money already spent (Credit Card/Debit).
* **`Bill`**: Use this for unpaid invoices (Accounts Payable).

#### Code Sample: Creating a Categorized Expense in Go

This demonstrates how to link a transaction to a specific `AccountRef` and `VendorRef`.

```go
type QBOPurchase struct {
    PaymentType string `json:"PaymentType"` // "CreditCard", "Cash", "Check"
    AccountRef  Reference `json:"AccountRef"`  // The Bank/CC account money came from
    Line        []Line    `json:"Line"`
    EntityRef   Reference `json:"EntityRef"`   // The Vendor
}

func (s *AIService) PostExpense(realmID string, vendorID string, accountID string, amount float64) error {
    expense := QBOPurchase{
        PaymentType: "CreditCard",
        AccountRef:  Reference{Value: "35"}, // "Checking Account" ID
        EntityRef:   Reference{Value: vendorID},
        Line: []Line{
            {
                Amount:     amount,
                DetailType: "AccountBasedExpenseLineDetail",
                AccountBasedExpenseLineDetail: &ExpenseDetail{
                    AccountRef: Reference{Value: accountID}, // The "Office Supplies" ID
                },
            },
        },
    }
    // Call our Batch or Single POST service
    return s.qboClient.CreatePurchase(realmID, expense)
}

```

---

### 4. The Attachable Service (The "Receipt Proof")

A Junior Accountant must attach the source document to the transaction. In QBO, this is a two-step process using the `Attachable` API.

1. **Upload**: Upload the binary (PDF/JPG) to the `/upload` endpoint. QBO returns an `AttachableID`.
2. **Link**: Send a `POST` to `/attachable` linking that ID to the `Bill` or `Purchase` ID created in the previous step.

---

### 5. Review & Feedback Loop

Since this is an "AI" accountant, the CPA must be able to "correct" it.

* **State Management**: Store transactions in your DB with a status: `PENDING_REVIEW`, `SYNCED`, or `FLAGGED`.
* **Feedback**: If a CPA changes an account from "Travel" to "Meals," your Go backend should capture that delta and use it to update your AI's prompt context for that specific `RealmID`.

This is the "Operational Excellence" phase. As a CTO, I know that your AI's reputation depends on its reliability. If a CPA sees an "Error 429" or a "Sync Failed" message, they lose trust in the "Junior Accountant."

Here is Phase 4: **Reliability & Compliance**.

---

## Phase 4: Reliability & Compliance

### 1. Mastering the Rate Limits

QuickBooks Online is strict. You have two primary ceilings to manage per `RealmID`:

* **Request Limit**: 500 requests per minute.
* **Concurrency Limit**: 10 simultaneous requests.

**The Strategy:** Implement a **Leaky Bucket** rate limiter in Go. This ensures your AI doesn't "burst" 50 requests at once when processing a large batch of receipts.

#### Code Sample: Leaky Bucket Limiter (using `uber-go/ratelimit`)

```go
import "go.uber.org/ratelimit"

type QBOWorker struct {
    limiter ratelimit.Limiter
}

func NewWorker() *QBOWorker {
    // 500 requests per 60 seconds ≈ 8 requests per second
    return &QBOWorker{
        limiter: ratelimit.New(8), 
    }
}

func (w *QBOWorker) SafeExecute(task func()) {
    w.limiter.Take() // Blocks until a slot is available
    task()
}

```

---

### 2. Handling the "Object Not Found" (Error 610)

This is the most common error for an AI Accountant. It happens when your "Shadow DB" is out of sync—e.g., the AI tries to use `AccountID: 45`, but the CPA just deleted it in the QBO UI.

**The "Junior Accountant" Logic:**

1. **Catch the 400/610 Error**: In your Go middleware.
2. **Trigger Immediate Sync**: Don't just fail. Call the `CDC` (Change Data Capture) for that specific entity type.
3. **Update Local Cache**: Mark the old ID as `inactive`.
4. **AI Re-categorization**: Tell the AI: "Account 45 is gone. Pick the next best match from the updated Chart of Accounts."

---

### 3. The "Idempotency" Pattern

If your Go backend crashes mid-request, you don't want the AI to create the same Invoice twice.

* **Vocabulary:** `RequestID` (or `client_ref` in some QBO objects).
* **Implementation:** Always generate a unique hash for every AI-generated transaction (e.g., `hash(vendor_id + date + amount)`). Store this in your Go DB. Before sending to QBO, check if a transaction with that hash already exists in your "Synced" table.

---

### 4. Audit Logging (CPA Compliance)

A CPA will eventually ask: *"Why did the AI put this Staples receipt into 'Legal Fees'?"*
Your Go backend must store a "Reasoning Path" for every write operation.

| Field | Description |
| --- | --- |
| `transaction_id` | The QBO ID created. |
| `ai_logic` | "Matched 'Staples' to Vendor #12. Categorized as 'Office' based on 90% confidence." |
| `source_doc` | Link to the `Attachable` PDF/Image. |
| `reviewed_by` | Null until the CPA clicks "Approve" in your UI. |

---

## Final Executive Summary of the Roadmap

1. **Phase 1 (Identity)**: Securely store encrypted OAuth2 tokens and handle multi-tenant routing.
2. **Phase 2 (Sync)**: Build a "Shadow DB" using Webhooks (Push) and CDC (Pull) to keep your AI's context fresh.
3. **Phase 3 (Intelligence)**: Map messy strings to the specific client's **Chart of Accounts** and use the **Attachable API** for receipts.
4. **Phase 4 (Reliability)**: Wrap everything in a **Rate Limiter** and implement **Self-Healing** logic for sync errors.


To implement the "Shadow DB" in Go, you need a schema that mirrors the QBO hierarchy while adding "AI-first" metadata fields (like confidence scores and reasoning).

Here is the technical design for your persistence layer.

---

## Phase 5: The Shadow DB Schema (Go + GORM)

We will use **GORM** (the standard Go ORM) to define these. Notice that every table includes a `RealmID` for multi-tenancy and a `SyncToken` for collision detection.

### 1. The Core Infrastructure Tables

These tables track the connection and the "brain" state of the AI for each client.

```go
// QBOConnection tracks the OAuth2 state for each CPA's client
type QBOConnection struct {
    RealmID           string    `gorm:"primaryKey"`
    AccessToken       string    `json:"-"` // Encrypted at rest
    RefreshToken      string    `json:"-"` // Encrypted at rest
    ExpiresAt         time.Time
    LastFullSyncAt    time.Time // Used for the "changedSince" CDC logic
    CompanyName       string
}

// ChartOfAccount mirrors QBO 'Account' entity
type ChartOfAccount struct {
    ID                string `gorm:"primaryKey"` // QBO ID
    RealmID           string `gorm:"index"`
    Name              string
    AccountType       string // e.g., 'Expense', 'Asset'
    AccountSubType    string // e.g., 'OfficeGeneralExpenses'
    Classification    string // 'BalanceSheet' or 'IncomeStatement'
    FullyQualifiedName string
    Active            bool
    SyncToken         string
}

```

### 2. The AI Context Tables (Vendors & Customers)

Your AI needs these to perform "Entity Resolution" (matching raw text like "STPL" to the Vendor "Staples").

```go
type Vendor struct {
    ID          string `gorm:"primaryKey"`
    RealmID     string `gorm:"index"`
    DisplayName string `gorm:"index"` // The human-readable name
    SyncToken   string
    // AI Metadata
    LastKnownAccountID string // AI suggestion: Usually map this vendor to this Account
    AISynonyms        string // JSON list: ["Staples Inc", "STAPLS", "Staples #44"]
}

```

---

### 3. The Transaction Layer (The AI's "Work-in-Progress")

This is the most critical part. You need a way to store transactions that the AI has *proposed* but the CPA hasn't *approved* yet.

```go
type ProposedTransaction struct {
    gorm.Model
    RealmID       string `gorm:"index"`
    SourceType    string // "Receipt", "BankFeed", "Email"
    RawAmount     float64
    RawDate       time.Time
    RawDescription string
    
    // AI Predictions
    PredictedVendorID  string
    PredictedAccountID string
    ConfidenceScore    float64 // 0.0 to 1.0
    AIReasoning        string  `gorm:"type:text"` // e.g. "Matched 'Shell' via synonym 'Shell Oil'"
    
    // QBO Link (Once synced)
    QBOTransactionID string `gorm:"index"`
    SyncStatus       string // "PENDING", "SYNCED", "ERROR", "REJECTED"
    ErrorMessage     string
}

```

---

## Technical Workflow: The "Upsert" Logic

When your Go backend receives data (from CDC or Webhooks), you should use a **Safe Upsert** pattern. This ensures you never have duplicate records and your `SyncToken` is always current.

```go
func (db *Database) UpsertAccount(account ChartOfAccount) error {
    return db.Clauses(clause.OnConflict{
        Columns:   []clause.Column{{Name: "id"}},
        DoUpdates: clause.AssignmentColumns([]string{"name", "active", "sync_token", "fully_qualified_name"}),
    }).Create(&account).Error
}

```

### Why this schema works for an AI Junior Accountant:

1. **Multi-Tenant Isolation**: The `RealmID` index ensures the AI never accidentally suggests an account from Client A to Client B.
2. **Fuzzy Matching Hooks**: The `AISynonyms` field in the `Vendor` table allows you to perform local text-matching before ever hitting the QBO API.
3. **Audit Trail**: The `AIReasoning` field provides the transparency CPAs demand.


To function like a human accountant, your Go backend needs a **Resolution Engine**. When a raw string like `"SQ * BAYSIDE PHO"` arrives from a bank feed, your AI shouldn't just guess; it should follow a deterministic path to find the correct `VendorID` in QBO.

### Phase 6: Entity Resolution (The Matcher)

We will use a **Multi-Stage Matching** strategy. This prevents your "Junior Accountant" from creating a new vendor every time a business name has a typo or a store number.

#### 1. The Matcher Logic Flow

1. **Normalization**: Strip special characters, convert to lowercase, and remove common "noise" (e.g., "Inc", "Corp", "LLC", "SQ *").
2. **Exact Match**: Check the `Vendor` table for an exact name match.
3. **Synonym Match**: Check the `AISynonyms` JSON/Array column for past known variations.
4. **Fuzzy Match**: If steps 1-3 fail, use a string distance algorithm like **Levenshtein** or **Jaro-Winkler**.
5. **AI Fallback**: If fuzzy confidence is low (< 0.7), send the name + Chart of Accounts to your LLM for a semantic guess.

#### 2. Implementation in Go

I recommend using a library like `github.com/agnivade/levenshtein` for the fuzzy logic.

```go
package service

import (
	"strings"
	"github.com/agnivade/levenshtein"
)

type MatchResult struct {
	VendorID   string
	Confidence float64 // 0.0 to 1.0
}

func (s *AIService) ResolveVendor(realmID string, rawName string) (MatchResult, error) {
	// 1. Normalize
	cleanName := strings.ToLower(strings.TrimSpace(rawName))
	
	// 2. Query local Shadow DB for Exact or Synonym match
	var vendor Vendor
	err := s.db.Where("realm_id = ? AND (display_name ILIKE ? OR ai_synonyms @> ?)", 
		realmID, cleanName, `["`+cleanName+`"]`).First(&vendor).Error
	
	if err == nil {
		return MatchResult{VendorID: vendor.ID, Confidence: 1.0}, nil
	}

	// 3. Fuzzy Search (Fetch candidates for the realm)
	var candidates []Vendor
	s.db.Where("realm_id = ?", realmID).Find(&candidates)

	bestMatch := MatchResult{}
	for _, c := range candidates {
		// Calculate similarity (1 - distance / max_length)
		dist := levenshtein.ComputeDistance(cleanName, strings.ToLower(c.DisplayName))
		maxLen := len(cleanName)
		if len(c.DisplayName) > maxLen {
			maxLen = len(c.DisplayName)
		}
		
		score := 1.0 - (float64(dist) / float64(maxLen))
		if score > bestMatch.Confidence {
			bestMatch = MatchResult{VendorID: c.ID, Confidence: score}
		}
	}

	// Threshold for "Junior Accountant" to act autonomously
	if bestMatch.Confidence > 0.85 {
		return bestMatch, nil
	}

	// 4. Return low confidence for Human/AI review
	return bestMatch, nil
}

```

---

### 3. Training the "Junior" (The Feedback Loop)

When a CPA manually corrects a transaction, your backend must "learn."

* **The Logic**: If the AI suggested `Vendor: 101` but the CPA changed it to `Vendor: 202`, your Go service should append the original raw string (`"SQ * BAYSIDE PHO"`) to the `AISynonyms` list for `Vendor: 202`.
* **The Result**: The next time that specific string appears, the Confidence becomes `1.0`.

---

### 4. Semantic Matching (Advanced)

For even better results, you can use **Vector Embeddings** (e.g., via `pgvector` in Postgres).

* You store the vector of the Vendor's name.
* You vectorize the incoming bank description.
* You perform a cosine similarity search in SQL. This catches "Bayside Vietnamese Restaurant" matching "Bayside Pho" because they are semantically related (food/Asian cuisine), even if the characters are different.


In accounting, a transaction without a receipt is just an unverified claim. For your AI Junior Accountant, the "Attachable" API is what provides the audit trail that CPAs demand.

Phase 7 focuses on the **Receipt Workflow**: how to take a raw file (PDF/JPEG) from your Go backend and link it to a specific `Bill` or `Purchase` in QBO.

---

## Phase 7: The Attachable Service (AP/AR Automation)

QuickBooks handles files via the **`Attachable`** entity. The most efficient way to do this is a **Single Multipart Request** that both uploads the file and links it to an existing transaction (like the `Bill` your AI just created).

### 1. The Multipart Request Structure

You must send two "parts" in one `POST` request to the `/upload` endpoint:

* **Part 1 (`file_metadata_01`)**: A JSON block defining which transaction the file belongs to.
* **Part 2 (`file_content_01`)**: The actual binary bytes of the image or PDF.

---

### 2. Implementation in Go

Go's `mime/multipart` package is powerful but verbose. Here is how you structure the AI's "Upload & Link" function.

```go
func (s *AIService) AttachReceipt(realmID string, entityType string, entityID string, filePath string) error {
    // 1. Prepare Metadata
    metadata := map[string]interface{}{
        "AttachableRef": []map[string]interface{}{
            {
                "EntityRef": map[string]string{
                    "type":  entityType, // e.g., "Purchase" or "Bill"
                    "value": entityID,   // The ID returned after your AI created the bill
                },
            },
        },
        "FileName": filepath.Base(filePath),
    }

    // 2. Create Multipart Body
    body := &bytes.Buffer{}
    writer := multipart.NewWriter(body)

    // Part 1: Metadata (JSON)
    metaPart, _ := writer.CreatePart(textproto.MIMEHeader{
        "Content-Disposition": []string{`form-data; name="file_metadata_01"; filename="metadata.json"`},
        "Content-Type":        []string{"application/json"},
    })
    json.NewEncoder(metaPart).Encode(metadata)

    // Part 2: File Content
    file, _ := os.Open(filePath)
    defer file.Close()
    filePart, _ := writer.CreatePart(textproto.MIMEHeader{
        "Content-Disposition": []string{fmt.Sprintf(`form-data; name="file_content_01"; filename="%s"`, filepath.Base(filePath))},
        "Content-Type":        []string{"application/pdf"}, // Or image/jpeg
    })
    io.Copy(filePart, file)
    writer.Close()

    // 3. Execute Request
    url := fmt.Sprintf("https://quickbooks.api.intuit.com/v3/company/%s/upload", realmID)
    req, _ := http.NewRequest("POST", url, body)
    req.Header.Set("Content-Type", writer.FormDataContentType())
    req.Header.Set("Authorization", "Bearer " + s.GetToken(realmID))

    resp, err := s.httpClient.Do(req)
    // ... handle err and status 200
    return err
}

```

---

### 3. Critical Vocabulary for Attachables

* **`EntityRef`**: This is the "Hook." It tells QBO exactly which object to pin the receipt to.
* **`Note`**: You can add a `Note` field to the metadata. I recommend putting the **AI's confidence score** or **OCR text summary** here so the CPA can see it without opening the file.
* **`Thumbnail`**: QBO automatically generates thumbnails, but only if you provide a valid `ContentType` (e.g., `image/png`).

---

### 4. The "AI-First" Strategy

To make your "Junior Accountant" truly elite, don't just upload the file:

1. **OCR Pre-processing**: Extract the Date, Total, and Tax *before* uploading.
2. **Duplicate Detection**: Before calling the `Attachable` API, check your "Shadow DB" for any existing transactions with the same `Amount` and `Date` for that `Vendor`. If it exists, link the new file to the *existing* transaction instead of creating a duplicate.
3. **Late Binding**: Sometimes your AI finds a receipt before the bank transaction appears. You can upload the `Attachable` without an `EntityRef` initially, and "bind" it to a transaction later when the bank feed clears.


To complete our roadmap, we move to the interface between your Go backend and the human expert. An AI Junior Accountant is only as good as its supervisor's ability to "Trust but Verify."

In this final phase, we build the **Review Loop**, which turns manual corrections into training data.

---

## Phase 8: The CPA Review Loop & Feedback System

### 1. The "Human-in-the-Loop" (HITL) Architecture

Your Go backend should not push high-stakes transactions (like large wire transfers or complex tax adjustments) directly to QBO without a "Pending" state in your DB.

* **Logic**: Every AI action is assigned a **Confidence Score** (0.0 to 1.0).
* **Thresholds**:
* **> 0.95**: Auto-sync to QBO (notify the CPA via digest).
* **0.70 - 0.95**: Mark as "Pending Review" in your dashboard.
* **< 0.70**: Flag as "Action Required" and ask for manual categorization.



---

### 2. Designing the Feedback UI

The CPA shouldn't just "fix" an error; their fix should update the AI's future behavior. In your Go API, create a specialized "Approval" endpoint.

**Go Endpoint: `POST /transactions/:id/approve**`

```go
func (s *AIService) ApproveTransaction(txID uint, corrections CorrectionPayload) error {
    // 1. Fetch the proposed transaction from our Shadow DB
    var tx ProposedTransaction
    s.db.First(&tx, txID)

    // 2. Check if the CPA changed the Category or Vendor
    if corrections.AccountID != tx.PredictedAccountID {
        // TRIGGER LEARNING: Save this as a new "Synonym" or "Rule"
        s.UpdateAILogic(tx.RealmID, tx.RawDescription, corrections.AccountID)
    }

    // 3. Final Sync to QBO
    qboID, err := s.qboClient.PostToQBO(tx.RealmID, corrections)
    if err != nil {
        return err
    }

    // 4. Update Status to SYNCED
    return s.db.Model(&tx).Updates(map[string]interface{}{
        "status": "SYNCED",
        "qbo_transaction_id": qboID,
    }).Error
}

```

---

### 3. The "AI Reasoning" Component

To build trust, your UI should display **why** the AI made a choice.

* **Good**: "Categorized as 'Office Supplies'."
* **Better**: "Categorized as 'Office Supplies' because 'Staples' matches 4 previous entries for this client."

---

### 4. Handling Reconciliation (The Final Exam)

The ultimate test for a Junior Accountant is the **Month-End Reconciliation**.

1. **AI Step**: Pull the `Report/BalanceSheet` and `Report/ProfitAndLoss` via QBO API.
2. **Detection**: AI flags transactions that don't match the bank statement (e.g., a $1,000 bill was paid twice).
3. **Presentation**: Show the CPA a "Discrepancy Report" with clear buttons: `Fix Automatically`, `Void Duplicate`, or `Ignore`.

---

## Your CTO's Checklist for Launch

* [ ] **Encryption**: Are `AccessToken` and `RefreshToken` encrypted with AES-256?
* [ ] **Rate Limiter**: Is your Go `leaky bucket` set to 500 requests/min?
* [ ] **Webhooks**: Does your server respond with `200 OK` in under 3 seconds?
* [ ] **Audit Trail**: Can you show a CPA exactly why the AI created a specific `JournalEntry`?


To build a professional-grade AI Junior Accountant, you need a project structure that separates your **Accounting Logic** from your **Integration Plumbery**.

In the Go world, we follow a standard layout often referred to as "Standard Project Layout" combined with Clean Architecture principles. This ensures that if you decide to add Xero or Sage later, you don't have to rewrite your AI.

---

## The AI Junior Accountant: Go Project Structure

```text
/qbo-ai-accountant
├── cmd/
│   └── server/
│       └── main.go           # Entry point: Initializes DB, Workers, and Server
├── internal/
│   ├── api/
│   │   ├── handler/          # HTTP Handlers (Webhooks, OAuth callbacks, AI endpoints)
│   │   └── middleware/       # Auth, Rate Limiting, QBO Token Refresh logic
│   ├── app/
│   │   ├── service/          # THE BRAIN: AI categorization, Entity Resolution
│   │   └── usecase/          # Business flows: "Process Receipt", "Reconcile Month"
│   ├── domain/
│   │   ├── model/            # GORM Structs (Account, Vendor, ProposedTransaction)
│   │   └── repository/       # Interface definitions for DB operations
│   ├── infrastructure/
│   │   ├── qbo/              # QBO Client: Raw API calls, Batching, Attachables
│   │   ├── db/               # GORM/Postgres setup and migrations
│   │   └── worker/           # Background Sync Workers (CDC, Webhooks Processor)
│   └── pkg/
│       ├── encryption/       # AES-256-GCM for tokens
│       └── ai/               # LLM/Vector search wrappers
├── configs/                  # environment variables (.env)
├── scripts/                  # DB migration scripts
├── go.mod
└── Makefile                  # Build and Run commands

```

---

## 1. Why this structure?

* **`cmd/`**: Keep it thin. It just wires everything together.
* **`internal/`**: This prevents other projects from importing your private logic.
* **`app/service/`**: This is where your AI lives. It doesn't care about HTTP; it just takes a "Raw Transaction" and returns a "Predicted Category."
* **`infrastructure/qbo/`**: This is your "Adapter." If the QBO API changes, you only fix code in this folder.

---

## 2. Key File: `internal/app/service/accountant.go`

This is the core of your "Junior Accountant." It coordinates between the AI and QBO.

```go
package service

import "yourproject/internal/domain/model"

type JuniorAccountant struct {
    qboClient   QBOClientInterface
    aiProvider  AIProviderInterface
    repo        TransactionRepo
}

func (s *JuniorAccountant) ProcessIncomingReceipt(realmID string, file []byte) error {
    // 1. Run AI OCR & Categorization
    prediction := s.aiProvider.AnalyzeReceipt(file)

    // 2. Resolve Vendor (from Phase 6)
    vendorID := s.ResolveVendor(realmID, prediction.VendorName)

    // 3. Save as "Proposed" in Shadow DB (from Phase 5)
    tx := model.ProposedTransaction{
        RealmID:    realmID,
        RawAmount:  prediction.Total,
        PredictedAccountID: prediction.CategoryID,
        Status: "PENDING_APPROVAL",
    }
    return s.repo.Save(&tx)
}

```

---

## 3. The "Service Registry" (Main Entry Point)

In `cmd/server/main.go`, you use Dependency Injection to connect the dots:

```go
func main() {
    db := database.Init()
    qbo := qbo.NewClient(config.ClientSecret)
    ai := ai.NewLLMClient(config.OpenAIKey)

    // Wire up the Junior Accountant service
    accountant := service.NewJuniorAccountant(qbo, ai, db)

    // Inject service into Handlers
    h := handler.NewHandler(accountant)
    
    // Start Server
    server.Run(h)
}

```
