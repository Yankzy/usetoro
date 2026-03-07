Product Requirements Document (PRD)

Epic: Toro Audit Armor (Defensible Position Engine)
Module: Toro Orchestration Platform / Fignode App
Objective: Empower CPAs to take aggressive, client-friendly tax positions by automatically generating legally cited, cryptographically sealed "Memos to File" at the point of transaction.

1. The Core Philosophy

Clients expect CPAs to defend gray-area expenses (travel, meals, retreats, vehicle use) against the IRS. To win an audit, the CPA needs Substantiation (receipts + business purpose) and Legal Precedent (Tax Code/Case Law). Toro automates the creation of both.

2. The UX Flow (Fignode Mobile & Web)

Action: When reviewing a high-risk transaction, the CPA taps a new action: [🛡️ Build Audit Defense].

State Change: The transaction state moves from PENDING to BUILDING_DEFENSE.

Client Interception: The system triggers the "Hound Agent" via SMS/Email to extract the IRS-required substantiation directly from the client (Who, What, Where, Why).

3. The RAG AI Pipeline (The "Tax Lawyer" Agent)

Once the client provides the context (e.g., "Dinner with potential franchisee"), the Toro AI initiates the Defense generation:

Step 1: Context Ingestion: Parse the transaction data (Date, Amount, Vendor) and the client's SMS response.

Step 2: Vector Search: Query the Toro Legal Vector DB (loaded with IRC Sec. 162, 274, and historic Tax Court cases).

Step 3: Memo Generation: Use the LLM to draft a formal "Memo to File."

Structure: 
1. Transaction Facts.
2. Client-Provided Business Purpose.
3. Relevant Tax Code Citation (e.g., "Under IRC §162(a), an ordinary and necessary expense...").
4. Conclusion of Deductibility.
4. The Cryptographic Vault

An audit happens 2-3 years after the transaction. The defense must be immutable.

Execution: The generated Memo, the raw SMS logs from the client, and the receipt image are combined into a single PDF.

Immutability: The PDF is hashed (e.g., SHA-256), and the hash is stored in the transaction_audit_logs table.

UI Delivery: A green shield icon [🛡️ Protected] appears next to the transaction in the ledger. If audited, the CPA simply clicks the shield to download the complete, timestamped legal defense packet.

5. Monetization Strategy (Advisory Tier)

This is a premium, Tier-2 feature.

Basic tier CPAs categorize transactions.

Advisory tier CPAs ($5,000/mo) provide Pre-Litigation Tax Strategy. Fignode allows them to tell clients: "We don't just file your taxes; we build a legal defense file for every aggressive deduction you take, ensuring you keep your money."