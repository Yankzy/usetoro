# Phase 3 Audit: The Accounting Intelligence (Pinecone Edition)

This document outlines the current implementation status of Phase 3 features as described in `go/qbo/road_map.md`.

## ✅ Implemented Features

### 1. CoA Vector Mapper
- **Status**: Implemented
- **Location**: `go/internal/services/ai/coa_mapper.go`
- **Details**:
  - Uses `PineconeClient` and `Embedder`.
  - Maps transaction descriptions to G/L accounts.
  - Filters by `entity_type: account`.
  - Sorting and threshold logic is present.

### 2. Entity Resolution Service
- **Status**: Implemented
- **Location**: `go/internal/services/ai/entity_resolver.go`
- **Details**:
  - Implements the 3-layer strategy:
    1.  **Local DB**: Exact/Synonym match.
    2.  **Pinecone**: Semantic match.
    3.  **Fuzzy**: Levenshtein verification.
  - Includes `Learn` method for feedback loop.

### 3. Pinecone Infrastructure
- **Status**: Implemented
- **Location**: `go/internal/infrastructure/vector/pinecone_client.go`
- **Details**:
  - Supports namespacing (RealmID).
  - Upsert and Query logic implemented.
  - Connection management handles.

### 4. Vector Sync Worker
- **Status**: Implemented
- **Location**: `go/internal/services/ai/vector_worker.go`
- **Details**:
  - Delta syncs Accounts, Vendors, and Customers to Pinecone.
  - Updates `vector_sync_state` in DB.

---

## ❌ Missing / To-Do Items

### 1. Transaction Creator Service (Business Logic)
- **Status**: **Missing High-Level Logic** (SDK methods exist)
- **Missing Components**:
  - **Decision Logic**: Logic to decide between creating a `Purchase` (paid) vs `Bill` (unpaid).
  - **`Reference` Struct**: A unified struct to map IDs from Pinecone (as described in roadmap).
  - **`PostExpense` Function**: A service-level function that orchestrates:
    1.  Resolving Vendor (EntityResolver).
    2.  Resolving Account (CoAMapper).
    3.  Creating the QBO Entity (Purchase/Bill).
- **Existing SDK Support**: `CreatePurchase` and `CreateBill` are available in `go/qbo/purchase.go` and `go/qbo/bill.go`.

### 2. Attachable Service (Receipt Workflow)
- **Status**: **Missing Service Logic** (SDK methods exist)
- **Missing Components**:
  - **Upload & Link Workflow**: Service logic to:
    1.  Accept a file (likely from HTTP request).
    2.  Upload it to QBO using `UploadAttachable`.
    3.  **Crucially**: Link it to the created transaction (`EntityRef`).
  - **OCR Pre-processing**: (Optional but mentioned) Extracting data before upload.
  - **Duplicate Detection**: Checking if receipt already exists.
- **Existing SDK Support**: `UploadAttachable` is available in `go/qbo/attachable.go`.

## Action Plan

1.  **Implement `TransactionService`**:
    -   Create `go/internal/services/accounting/transaction_service.go`.
    -   Implement `PostExpense` logic using `CoAMapper` and `EntityResolver`.
    -   Use `CreatePurchase` / `CreateBill` from QBO SDK.

2.  **Implement `AttachableService`**:
    -   Create `go/internal/services/accounting/attachable_service.go`.
    -   Implement `UploadReceipt` logic that calls `client.UploadAttachable`.
    -   Ensure linking logic (`EntityRef`) is handled.

3.  **Integration**:
    -   Connect these services to the main API/Worker.
