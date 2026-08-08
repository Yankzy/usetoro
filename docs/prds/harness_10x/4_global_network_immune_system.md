# Product Requirements Document (PRD)

## 4. Global Network Immune System (Cross-Tenant Pattern Sharing)

**Document Reference:** `docs/prds/harness_10x/4_global_network_immune_system.md`  
**Status:** Draft v1.0  
**Owner:** Security & Privacy Engineering  
**Subsystem:** System 4 of the 5 Core Harness Subsystems (Global Adaptation Layer)

---

# 1. Executive Summary & Core Objective

The **Global Network Immune System** extends local learning (human feedback rule extraction) into network-wide pattern propagation across enterprise tenants.

### Core Objective
*When Tenant A discovers a new fraud pattern, tax evasion signature, or vendor anomaly, every other company on the network becomes instantly immune—without exposing Tenant A's private data or PII.*

---

# 2. Zero-Knowledge Pattern Sharing Architecture

Multi-tenancy isolation (`realm_id`, `tenant_id`) is preserved using **Differential Privacy & Anonymized Signatures**:

```
Tenant A (Local Execution)
  │
  ├─► Human Correction / Backtrack
  ├─► Abstract Pattern Extractor
  ▼
Anonymized Anomaly Signature (Hashes, Category Offsets, Feature Ratios)
  │
  ▼
NATS Global Immune Network Bus (`network.immunity.broadcast`)
  │
  ├───────────────────────┼───────────────────────┐
  ▼                       ▼                       ▼
Tenant B               Tenant C               Tenant D
(Prior Probabilities   (Prior Probabilities   (Prior Probabilities
 Updated Instantly)     Updated Instantly)     Updated Instantly)
```

---

# 3. Anomaly Signature Format

```json
{
  "signature_id": "sig_fraud_vat_carousel_991",
  "domain": "accounting.ap",
  "anomaly_type": "BANK_DETAILS_CHANGE_AFTER_INVOICE",
  "feature_ratios": {
    "amount_variance": 0.05,
    "time_delta_hours": 2.4,
    "new_bank_country_mismatch": true
  },
  "prior_probability_adjustment": 0.85,
  "anonymized_at": "2026-08-06T15:30:00Z"
}
```

When Tenant B processes a transaction matching this feature signature, the Decision System automatically increases the dynamic cost of *Continue automatically* and boosts the score of *Ask supplier confirmation*.
