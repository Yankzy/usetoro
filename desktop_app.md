# Fignode Pro: Desktop Application Specification

**Target Users:** CPAs, Accountants, Bookkeepers  
**Tech Stack:** Tauri (Rust) + React (Shared with Web)  
**Role:** The Command Center for the "AI Staff"

## 1. Core Philosophy: "The Control Tower"

The Desktop App is not for data entry; it is for **Orchestration**. The CPA is the Pilot; the Agents are the crew. The app allows the CPA to assign work, monitor accuracy, and intervene only when necessary.

## 2. Feature Set: Managing the "Agent Hive"

### 2.1 The "Staffing" Dashboard (Hire & Fire)

A drag-and-drop interface to instantiate new Agents for specific clients.

- **"Hire" an Agent:** Click "Add Bookkeeper" -> Select Client ("Bob's Trucking") -> Deploy.
- **Configuration:**
  - Set Aggression Level (e.g., "Chase client every 3 days" vs "Every 7 days").
  - Set Authority Limits (e.g., "Auto-categorize transactions under $500").
- **Status Monitor:** See real-time status of every agent (e.g., "🟢 Chaser Agent: Active - Waiting for reply from Bob").

### 2.2 The "Triage" Feed (Human-in-the-Loop)

This is the high-velocity review screen where the CPA corrects the AI.

- **The Queue:** A unified stream of "Low Confidence" decisions from all Agents across all clients.
- **The Interaction:**
  - **Agent:** "I think this Home Depot receipt is 'Job Supplies' but it might be 'Repairs'. Confidence: 75%."
  - **CPA Action:** Press 1 for Supplies, 2 for Repairs.
- **The Feedback Loop:** Every click trains the Agent for that specific client.

### 2.3 The "Watch Folder" (Local Bridge)

- **Feature:** The app monitors local directories (e.g., `~/Clients/Invoices`).
- **Action:** When a file is saved there, the Desktop App instantly uploads it to the Go Gate.
- **Routing:** The user can right-click a folder and say "Assign to Agent: Invoice Parser."

## 3. Feature Set: Client Communication (The "Chaser")

### 3.1 Unified Inbox (WhatsApp + SMS + Email)

- **Aggregated Feed:** View all client conversations in one timeline, regardless of channel.
- **Agent Participation:** See the "Chaser Agent" asking for receipts in real-time.
- **Human Override:** The CPA can type a message to "Interrupt" the Agent and take over the conversation seamlessly.

### 3.2 "Ask the Agent" (Internal Chat)

- **Context:** A sidebar chat where the CPA talks to the Protocol Brain.
- **Commands:**
  - "@Bookkeeper, re-run the reconciliation for March."
  - "@Analyst, show me the burn rate for Client X."
- **Result:** The Desktop App streams the Agent's "Thought Process" via the gRPC/WebSocket pipe.

## 4. Feature Set: The "Shadow Ledger" (Data Viewer)

### 4.1 Real-Time Sync View

- **Status:** Shows the health of the connection to QuickBooks/Plaid.
- **Conflict Resolution:** If QBO data conflicts with Toro data, the Desktop App flags it for review.

### 4.2 The "Source of Truth" Viewer

- **Dual View:** Click a transaction line to see the original source evidence side-by-side (Receipt Image + GPS Map + Chat Log).
- **Why:** QuickBooks only shows the numbers. Fignode shows the proof.

## 5. Technical Requirements (Desktop Specific)

- **System Tray Agent:** The app must run in the background (System Tray) to keep watching folders even when the window is closed.
- **Native Notifications:** Use OS-level notifications for "Urgent" Agent alerts (e.g., "Payroll Deadline in 1 hour").
- **Offline Mode:** Queue file uploads and decisions if the internet disconnects; sync when online.

---
## Toro Chat (The Commerce Chat)

**Version:** 1.0
**Target:** A "WeChat for Business" where Money and Agents are native primitives.

## 1. Executive Summary

Toro Chat is not just a messaging app; it is a **Transaction Browser**.
Unlike WhatsApp or Slack, which treat text as the primary unit of value, Toro Chat treats **Structured Financial Objects** (Invoices, Payments, Contracts) as first-class citizens.

**Core Value Proposition:**

- **Shared State:** When User A pays an invoice, the message bubble updates for User B instantly.
- **Agent Participation:** AI Agents live in the chat, drafting documents and executing payments on command.
- **Universal Sync:** Every action in the chat automatically updates the underlying ledger (QuickBooks/NetSuite) for all parties.

## 2. The Data Model: "Smart Blocks"

We are moving beyond `text/plain`. The chat engine must support **Structured Message Types**.

### 2.1 The Message Schema (Postgres/JSONB)

Every message row in the database follows this polymorphic structure:

```sql
CREATE TABLE messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    conversation_id UUID NOT NULL,
    sender_id UUID NOT NULL,
    
    -- The Core Primitive
    msg_type TEXT NOT NULL, -- 'text', 'invoice', 'payment', 'contract', 'request'
    
    -- The Payload (Flexible JSONB)
    content JSONB NOT NULL, 
    
    -- The Dynamic State (Syncs across devices)
    metadata JSONB DEFAULT '{}', -- e.g., { "status": "paid", "paid_at": "..." }
    
    created_at TIMESTAMPTZ DEFAULT NOW()
);
```

### 2.2 Defined "Smart Block" Types

**Type: `invoice`**

- **Payload:** `{ "amount": 5000, "currency": "USD", "items": ["Consulting"], "due_date": "2026-02-01" }`
- **Visual Representation:** A card showing the amount and due date.
- **Action Button:** `[PAY NOW]` (Visible to receiver).
- **State Transitions:** `unpaid` -> `processing` -> `paid`.

**Type: `payment`**

- **Payload:** `{ "amount": 5000, "method": "ach", "reference": "txn_999" }`
- **Visual Representation:** A green "Success" receipt.
- **Side Effect:** Updates the linked invoice block to paid.

**Type: `agent_action`**

- **Payload:** `{ "action": "draft_contract", "status": "thinking" }`
- **Visual Representation:** A "Typing..." indicator or a Skeleton Loader.
- **Update:** Replaced by the final contract block once generated.

## 3. Architecture: Real-Time Sync (Go + NATS)

We need sub-100ms latency for state updates. "Green Bubble" (SMS) fallback logic is handled here.

### 3.1 The WebSocket Gateway (Go)

- **Connection:** `wss://api.usetoro.io/chat/v1/stream`
- **Auth:** JWT Token (User ID).
- **Responsibility:**
  - Maintain persistent connection to Mobile/Web clients.
  - Subscribe to NATS Subject `chat.room.{conversation_id}`.
  - Push new messages and state updates (`UPDATE_MESSAGE` events) to the client.

### 3.2 The State Engine (Go Protocol)

When a user clicks "Pay" on an invoice block:

1. **Client:** Sends `POST /api/chat/action/pay` with `message_id`.
2. **Go Protocol:**
   - Validates funds/permissions.
   - Executes payment via Stripe/ACH.
   - **Crucial:** Publishes `event.message.updated` to NATS.
3. **WebSocket Gateway:** Pushes the update to all participants in the room.
4. **Result:** Both phones see the invoice turn green simultaneously.

### 3.3 The "Green Bubble" Bridge (Twilio)

If the recipient does not have the app:

- **Logic:** Check `users` table. Is `recipient_phone` registered?
- **If No:** Send SMS via Twilio.
- **Body:** "Bob sent you an invoice for $5,000. Tap to view & pay: https://www.google.com/search?q=https://toro.link/inv/xyz"
- **The Web View:** The link opens a Progressive Web App (PWA) version of the chat. They can pay as a guest.

## 4. Mobile Implementation (React Native)

The frontend must render these blocks natively.

### 4.1 Component Architecture

We use a Renderer Pattern for the chat list.

```typescript
// MessageList.tsx
const renderMessage = (msg: Message) => {
  switch (msg.type) {
    case 'text':
      return <TextBubble content={msg.content} />;
    case 'invoice':
      return <InvoiceBlock data={msg.content} status={msg.metadata.status} />;
    case 'contract':
      return <ContractBlock url={msg.content.url} />;
    default:
      return <UnknownBlock />;
  }
};
```

### 4.2 Optimistic Updates

To feel instant:

1. **User clicks** "Pay".
2. **UI:** Immediately turns the block "Yellow" (Processing).
3. **Network:** Sends request.
4. **Socket:** Receives "Paid" event -> UI turns "Green".

## 5. The Agent Integration ("The Third User")

Agents are simply users with a flag `is_bot: true`.

### 5.1 The Mention System

- **Trigger:** User types `@Bookkeeper`.
- **Flow:**
  1. Frontend sends text to Go Gate.
  2. Go Gate detects `@` mention.
  3. Forwards context to Python Hand via NATS.
  4. Python Hand (LLM) generates response (e.g., specific text or a Smart Block).
  5. Agent "posts" the response to the chat like a normal user.

### 5.2 Autonomous Action

Agents can initiate chats.

- **Scenario:** Invoice is 5 days overdue.
- **Agent Logic:** Wakes up (Cron), checks Ledger.
- **Action:** Posts a message in the `{Business + Client}` chat:
  > "Hi @Client, gentle reminder that this invoice is due. Click below to pay."
  > [Invoice Block: Resent]

## 6. Execution Roadmap

### Phase 1: The "Smart" MVP (Internal)

- Build: 1-to-1 Chat with Text + Image support.
- Build: The "Invoice Block" (Create & Pay).
- **Goal:** Replace internal email for 5 CPA firms.

### Phase 2: The SMS Bridge (Viral Loop)

- Build: Twilio fallback logic.
- Build: "Guest View" Web App for payment.
- **Goal:** Allow users to bill clients who don't have the app yet.

### Phase 3: The Agent Layer

- Build: `@Agent` mentioning logic.
- Build: Agent-initiated reminders.
- **Goal:** Reduce "Chasing" time by 80%.

## 7. Security & Compliance (Banking Grade)

**End-to-End Encryption (E2EE):**

- **Note:** We cannot use Signal-style absolute E2EE because the Agents need to read the chat to work.
- **Solution:** Server-Side Encryption. Keys managed by Toro (or Customer via BYOK KMS). Data encrypted at rest in Postgres.

**Audit Trail:**

- Every "Smart Action" (Pay, Sign, Approve) creates an immutable log entry linked to the message ID.
- This log is visible to the CPA for compliance.

## 8. Summary for Developers

- **Backend:** Go (WebSockets + NATS).
- **Database:** Postgres (JSONB for message payloads).
- **Frontend:** React Native (Mobile) + React (Web).
- **Protocol:** WebSocket for live sync; HTTP for heavy actions (uploading files).
- **Constraint:** Do not build a generic chat app. If a feature doesn't help money move or work get done, cut it. No stickers. No stories. Just business.

![Fignode Pro](./fignode%20pro.png)

