### **Ken**
"If you want to build **Google Workspace Primitives** that are truly composable for a multi-tenant platform like Toro, you need to stop thinking about your own account and start thinking about **Managed Capabilities**. 

A PRD for these primitives isn't just a list of features; it’s a specification for how an AI agent can securely borrow a human's digital workspace to get work done."

---

## PRD: Toro Google Workspace Primitives (v1.0)

### **1. Executive Summary**
Toro aims to commoditize the Google Workspace ecosystem into a set of **NATS-native Agentic Primitives**. These primitives allow internal and external agents to perform structured operations (Read/Write/Notify) across Gmail, Drive, Sheets, and Calendar without managing complex OAuth flows or individual API client libraries.

### **2. Core Primitive Definitions**

#### **Primitive A: The 'Watcher' (Ingress Source)**
- **Definition:** A persistent listener that converts Workspace events into FIPA `INFORM` envelopes.
- **Composable Action:** `agents.io.google.watch`
- **Capabilities:**
    - **Gmail Watch:** Emit event when new mail matches a specific filter (e.g., `label:invoice`).
    - **Drive Watch:** Emit event when a file is created or updated in a shared folder.
    - **Sheets Watch:** Emit event when a specific range of cells is modified.

#### **Primitive B: The 'Vault' (Managed Storage)**
- **Definition:** A structured interface for agents to persist and retrieve binary data using Google Drive as the backend.
- **Composable Action:** `agents.io.google.drive`
- **Capabilities:**
    - `STORE_FILE`: Uploads a blob, returns a Toro File ID + Drive URL.
    - `RETRIEVE_FILE`: Downloads a blob via ID for OCR processing.
    - `GRANT_ACCESS`: Dynamically adds a user's email to a file’s ACL (Access Control List).

#### **Primitive C: The 'Canvas' (Shared Memory)**
- **Definition:** Using Google Sheets as a human-readable state machine that agents and humans can edit simultaneously.
- **Composable Action:** `agents.io.google.sheets`
- **Capabilities:**
    - `UPSERT_ROW`: Matches an `entity_id` and updates columns.
    - `READ_RANGE`: Grabs context for an LLM reasoning loop.
    - `FORMAT_LEDGER`: Applies deterministic styling to raw accounting data.

---

### **3. Technical Architecture (The 'Adapter' Model)**



1.  **Identity Layer:** Toro manages a master 'Service Account' or 'Marketplace App' that handles the heavy lifting of OAuth refresh tokens for all connected users.
2.  **The Adapter Worker:** A deterministic Go Worker that listens to `tasks.io.google.>` and translates FIPA envelopes into Google REST API calls.
3.  **The Proof Loop:** Every successful Google action returns a FIPA `INFORM` proof containing the Google resource ID (e.g., File ID, Message ID).

### **4. Security & Multi-Tenancy**
- **Token Isolation:** Each user's OAuth credentials are encrypted in Toro's `Vault` and only decrypted in-memory by the Adapter Worker during a specific task execution.
- **Internal Only Flag:** Allow developers to restrict their Google primitives to *only* operate within their own Workspace organization for internal safety.

---

### **Elon**
"This is exactly it. You are abstracting the friction of the 'Old Web' (REST/OAuth) so agents can live in the 'New Web' (Event-driven/Agentic). 

If a developer building on Toro needs to send an email, they shouldn't have to learn the Gmail API. They just send a message to the `agents.io.google.gmail` primitive. You are becoming the standardized interface for digital work. If you build this, Toro becomes the OS that runs on top of Google's data."

### **Mark**
"And from a growth perspective, these primitives are your **Viral Expansion Loop**. 

When a Toro agent creates a Google Sheet for a client, that client sees 'Powered by Toro' at the bottom of the sheet. When they see how much faster their business runs because their 'Canvas' is being updated by an autonomous agent, they'll want to build their *own* agents. You are leveraging Google's massive 3-billion-user footprint to distribute your protocol."

---

**Yankz, which primitive do you want your engineering team (you) to implement first: the 'Watcher' to automate ingestion, or the 'Canvas' to give your agents a shared space to work with humans?**