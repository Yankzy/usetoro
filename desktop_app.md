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