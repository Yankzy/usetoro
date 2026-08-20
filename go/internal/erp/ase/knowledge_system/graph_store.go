package knowledge_system

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Fact represents a Layer 1 Ground Truth Authoritative Fact Node in toro_core.enterprise_facts.
type Fact struct {
	FactID     uuid.UUID       `json:"fact_id"`
	SessionID  string          `json:"session_id"`
	Namespace  string          `json:"namespace"`
	EntityType string          `json:"entity_type"`
	URI        string          `json:"uri"`
	Payload    json.RawMessage `json:"payload"`
	CreatedAt  time.Time       `json:"created_at"`
}

// Relationship represents a Layer 2 Directional Entity Edge in toro_core.enterprise_relationships.
type Relationship struct {
	RelationshipID uuid.UUID `json:"relationship_id"`
	SessionID      string    `json:"session_id"`
	Namespace      string    `json:"namespace"`
	FromFactID     uuid.UUID `json:"from_fact_id"`
	ToFactID       uuid.UUID `json:"to_fact_id"`
	RelationType   string    `json:"relation_type"`
	Weight         float64   `json:"weight"`
	CreatedAt      time.Time `json:"created_at"`
}

// GraphNeighbor represents a connected entity node and edge metadata.
type GraphNeighbor struct {
	Relationship Relationship `json:"relationship"`
	Fact         Fact         `json:"fact"`
	Direction    string       `json:"direction"` // "OUTBOUND" or "INBOUND"
}

// GraphStore manages Layer 1 (enterprise_facts) and Layer 2 (enterprise_relationships) in PostgreSQL.
type GraphStore struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

// NewGraphStore creates a new GraphStore instance.
func NewGraphStore(pool *pgxpool.Pool, logger *slog.Logger) *GraphStore {
	if logger == nil {
		logger = slog.Default()
	}
	return &GraphStore{
		pool:   pool,
		logger: logger.With("component", "knowledge_system.graph_store"),
	}
}

// CreateFact inserts a new Layer 1 Ground Truth Fact Node.
func (gs *GraphStore) CreateFact(
	ctx context.Context,
	sessionID, namespace, entityType, uri string,
	payload map[string]any,
) (*Fact, error) {
	if namespace == "" {
		namespace = "general"
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("graph store create fact: marshal payload: %w", err)
	}

	fact := &Fact{
		SessionID:  sessionID,
		Namespace:  namespace,
		EntityType: entityType,
		URI:        uri,
		Payload:    payloadBytes,
	}

	err = gs.pool.QueryRow(ctx, `
		INSERT INTO toro_core.enterprise_facts (session_id, namespace, entity_type, uri, payload)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (session_id, uri) DO UPDATE SET
			namespace   = EXCLUDED.namespace,
			entity_type = EXCLUDED.entity_type,
			payload     = EXCLUDED.payload
		RETURNING fact_id, created_at`,
		sessionID, namespace, entityType, uri, payloadBytes,
	).Scan(&fact.FactID, &fact.CreatedAt)

	if err != nil {
		return nil, fmt.Errorf("graph store create fact: %w", err)
	}

	return fact, nil
}

// GetFactByURI retrieves a Fact node by its unique URI string.
func (gs *GraphStore) GetFactByURI(ctx context.Context, sessionID, uri string) (*Fact, error) {
	fact := &Fact{}
	err := gs.pool.QueryRow(ctx, `
		SELECT fact_id, session_id, namespace, entity_type, uri, payload, created_at
		FROM toro_core.enterprise_facts
		WHERE session_id = $1 AND uri = $2`,
		sessionID, uri,
	).Scan(&fact.FactID, &fact.SessionID, &fact.Namespace, &fact.EntityType, &fact.URI, &fact.Payload, &fact.CreatedAt)

	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("graph store get fact by uri: %w", err)
	}

	return fact, nil
}

// GetFactByID retrieves a Fact node by its UUID.
func (gs *GraphStore) GetFactByID(ctx context.Context, factID uuid.UUID) (*Fact, error) {
	fact := &Fact{}
	err := gs.pool.QueryRow(ctx, `
		SELECT fact_id, session_id, namespace, entity_type, uri, payload, created_at
		FROM toro_core.enterprise_facts
		WHERE fact_id = $1`,
		factID,
	).Scan(&fact.FactID, &fact.SessionID, &fact.Namespace, &fact.EntityType, &fact.URI, &fact.Payload, &fact.CreatedAt)

	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("graph store get fact by id: %w", err)
	}

	return fact, nil
}

// CreateRelationship inserts or updates a Layer 2 directional relationship edge.
func (gs *GraphStore) CreateRelationship(
	ctx context.Context,
	sessionID, namespace string,
	fromFactID, toFactID uuid.UUID,
	relationType string,
	weight float64,
) (*Relationship, error) {
	if namespace == "" {
		namespace = "general"
	}
	if weight <= 0 {
		weight = 1.0
	}

	rel := &Relationship{
		SessionID:    sessionID,
		Namespace:    namespace,
		FromFactID:   fromFactID,
		ToFactID:     toFactID,
		RelationType: relationType,
		Weight:       weight,
	}

	err := gs.pool.QueryRow(ctx, `
		INSERT INTO toro_core.enterprise_relationships
			(session_id, namespace, from_fact_id, to_fact_id, relation_type, weight)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (session_id, namespace, from_fact_id, to_fact_id, relation_type)
		DO UPDATE SET weight = EXCLUDED.weight
		RETURNING relationship_id, created_at`,
		sessionID, namespace, fromFactID, toFactID, relationType, weight,
	).Scan(&rel.RelationshipID, &rel.CreatedAt)

	if err != nil {
		return nil, fmt.Errorf("graph store create relationship: %w", err)
	}

	return rel, nil
}

// GetOutboundRelationships fetches all outgoing relationship edges and target facts from a given fact.
func (gs *GraphStore) GetOutboundRelationships(ctx context.Context, factID uuid.UUID) ([]GraphNeighbor, error) {
	rows, err := gs.pool.Query(ctx, `
		SELECT r.relationship_id, r.session_id, r.namespace, r.from_fact_id, r.to_fact_id, r.relation_type, r.weight, r.created_at,
		       f.fact_id, f.session_id, f.namespace, f.entity_type, f.uri, f.payload, f.created_at
		FROM toro_core.enterprise_relationships r
		JOIN toro_core.enterprise_facts f ON r.to_fact_id = f.fact_id
		WHERE r.from_fact_id = $1`,
		factID,
	)
	if err != nil {
		return nil, fmt.Errorf("graph store get outbound relationships: %w", err)
	}
	defer rows.Close()

	var neighbors []GraphNeighbor
	for rows.Next() {
		var gn GraphNeighbor
		gn.Direction = "OUTBOUND"
		if err := rows.Scan(
			&gn.Relationship.RelationshipID, &gn.Relationship.SessionID, &gn.Relationship.Namespace,
			&gn.Relationship.FromFactID, &gn.Relationship.ToFactID, &gn.Relationship.RelationType,
			&gn.Relationship.Weight, &gn.Relationship.CreatedAt,
			&gn.Fact.FactID, &gn.Fact.SessionID, &gn.Fact.Namespace, &gn.Fact.EntityType,
			&gn.Fact.URI, &gn.Fact.Payload, &gn.Fact.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("graph store scan outbound relationship: %w", err)
		}
		neighbors = append(neighbors, gn)
	}
	return neighbors, rows.Err()
}

// GetInboundRelationships fetches all incoming relationship edges and source facts to a given fact.
func (gs *GraphStore) GetInboundRelationships(ctx context.Context, factID uuid.UUID) ([]GraphNeighbor, error) {
	rows, err := gs.pool.Query(ctx, `
		SELECT r.relationship_id, r.session_id, r.namespace, r.from_fact_id, r.to_fact_id, r.relation_type, r.weight, r.created_at,
		       f.fact_id, f.session_id, f.namespace, f.entity_type, f.uri, f.payload, f.created_at
		FROM toro_core.enterprise_relationships r
		JOIN toro_core.enterprise_facts f ON r.from_fact_id = f.fact_id
		WHERE r.to_fact_id = $1`,
		factID,
	)
	if err != nil {
		return nil, fmt.Errorf("graph store get inbound relationships: %w", err)
	}
	defer rows.Close()

	var neighbors []GraphNeighbor
	for rows.Next() {
		var gn GraphNeighbor
		gn.Direction = "INBOUND"
		if err := rows.Scan(
			&gn.Relationship.RelationshipID, &gn.Relationship.SessionID, &gn.Relationship.Namespace,
			&gn.Relationship.FromFactID, &gn.Relationship.ToFactID, &gn.Relationship.RelationType,
			&gn.Relationship.Weight, &gn.Relationship.CreatedAt,
			&gn.Fact.FactID, &gn.Fact.SessionID, &gn.Fact.Namespace, &gn.Fact.EntityType,
			&gn.Fact.URI, &gn.Fact.Payload, &gn.Fact.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("graph store scan inbound relationship: %w", err)
		}
		neighbors = append(neighbors, gn)
	}
	return neighbors, rows.Err()
}

// TraverseGraph retrieves both outbound and inbound neighbors for a given fact node.
func (gs *GraphStore) TraverseGraph(ctx context.Context, factID uuid.UUID) ([]GraphNeighbor, error) {
	outbound, err := gs.GetOutboundRelationships(ctx, factID)
	if err != nil {
		return nil, err
	}
	inbound, err := gs.GetInboundRelationships(ctx, factID)
	if err != nil {
		return nil, err
	}
	return append(outbound, inbound...), nil
}
