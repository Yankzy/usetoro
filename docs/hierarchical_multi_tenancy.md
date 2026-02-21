# Architectural Spec: Go-Native Hierarchical Multi-Tenancy

**Objective:** Architect a high-performance "HoldCo/OpCo" tree structure using Go, pgx, and Postgres Recursive CTEs to power CPA M&A rollups.

---

## 1. The Postgres Schema (The Tree)

We drop the ORM completely. We use a raw SQL schema with a self-referencing foreign key.

```sql
CREATE TABLE entities (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    parent_id UUID REFERENCES entities(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    entity_type VARCHAR(50) NOT NULL, -- 'apex_cpa', 'sub_cpa', 'client'
    branding_logo_url TEXT,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- Critical Index for Tree Traversal
CREATE INDEX idx_entities_parent_id ON entities(parent_id);
```

---

## 2. The Go Domain Models

Strictly typed Go structs to represent the nodes in our memory space.

```go
package domain

import (
	"time"
	"github.com/google/uuid"
)

type EntityType string

const (
	TypeApexCPA EntityType = "apex_cpa" // The HoldCo
	TypeSubCPA  EntityType = "sub_cpa"  // The Acquired Firm
	TypeClient  EntityType = "client"   // The Trucker/SMB
)

type Entity struct {
	ID              uuid.UUID  `json:"id"`
	ParentID        *uuid.UUID `json:"parent_id,omitempty"` // Pointer to handle NULL
	Name            string     `json:"name"`
	Type            EntityType `json:"type"`
	BrandingLogoURL *string    `json:"branding_logo_url,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}
```

---

## 3. The Recursive Query (The Core Engine)

When an Apex CPA logs into Fignode Pro, they need to see all their subsidiaries and all the truckers underneath those subsidiaries.

We do not do this with a `for` loop in Go (which causes N+1 query death). We let Postgres do the heavy lifting using a `WITH RECURSIVE` query.

```go
package repository

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type EntityRepo struct {
	db *pgxpool.Pool
}

// GetEntityTree fetches the Root entity and EVERY child/grandchild beneath it in ONE query.
func (r *EntityRepo) GetEntityTree(ctx context.Context, rootID uuid.UUID) ([]Entity, error) {
	query := `
		WITH RECURSIVE entity_tree AS (
			-- Base Case: The Root Node (Apex CPA)
			SELECT id, parent_id, name, entity_type, branding_logo_url, created_at
			FROM entities
			WHERE id = $1

			UNION ALL

			-- Recursive Step: Find all children of the nodes currently in the tree
			SELECT e.id, e.parent_id, e.name, e.entity_type, e.branding_logo_url, e.created_at
			FROM entities e
			INNER JOIN entity_tree et ON e.parent_id = et.id
		)
		SELECT id, parent_id, name, entity_type, branding_logo_url, created_at 
		FROM entity_tree;
	`

	rows, err := r.db.Query(ctx, query, rootID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tree []Entity
	for rows.Next() {
		var e Entity
		err := rows.Scan(&e.ID, &e.ParentID, &e.Name, &e.Type, &e.BrandingLogoURL, &e.CreatedAt)
		if err != nil {
			return nil, err
		}
		tree = append(tree, e)
	}

	return tree, nil
}
```

---

## 4. Cascading Rule Resolution in Go

When evaluating a transaction for Dave's Trucking (Level 3), you pull the entity tree upwards to the Apex Node (Level 1).

```go
// Psuedo-logic for Rule Resolution
func ResolveRulesForTransaction(ctx context.Context, tx Transaction, clientID uuid.UUID) {
    // 1. Fetch the lineage (Client -> Sub CPA -> Apex CPA)
    lineage := GetEntityLineageUpwards(ctx, clientID) 
    
    // 2. Iterate from bottom to top. 
    // This ensures Client rules override Sub rules, which override Apex rules.
    for _, entity := range lineage {
        rules := FetchRulesForEntity(ctx, entity.ID)
        
        for _, rule := range rules {
            if isMatch, explanation := rule.Evaluate(tx); isMatch {
                // MATCH FOUND! 
                // Publish to NATS: ledger.transaction.categorized
                PublishCategorizedEvent(tx, explanation)
                return 
            }
        }
    }
    
    // 3. No rules matched in the entire tree. Send to LLM/Tiyakin via NATS.
    PublishToLLMQueue(tx)
}
```

---

## 5. Middleware Data Isolation (Security)

Every single HTTP request to the Fignode API must pass through an RBAC middleware that verifies the user's `session_entity_id` is an ancestor of the `target_entity_id` they are trying to access. If a Sub CPA tries to view the Apex CPA's master dashboard, the Go middleware instantly returns a `403 Forbidden`.