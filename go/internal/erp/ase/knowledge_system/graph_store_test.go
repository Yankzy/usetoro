package knowledge_system

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestFact_StructFields(t *testing.T) {
	factID := uuid.New()
	now := time.Now().UTC()

	fact := &Fact{
		FactID:     factID,
		SessionID:  "realm_123",
		Namespace:  "accounting",
		EntityType: "invoice",
		URI:        "fact:accounting:invoice:001",
		Payload:    []byte(`{"amount": 100.50}`),
		CreatedAt:  now,
	}

	if fact.FactID != factID {
		t.Fatalf("expected factID %v, got %v", factID, fact.FactID)
	}
	if fact.SessionID != "realm_123" {
		t.Fatalf("expected realm_123, got %s", fact.SessionID)
	}
	if fact.URI != "fact:accounting:invoice:001" {
		t.Fatalf("expected fact:accounting:invoice:001, got %s", fact.URI)
	}
}

func TestRelationship_StructFields(t *testing.T) {
	relID := uuid.New()
	fromID := uuid.New()
	toID := uuid.New()
	now := time.Now().UTC()

	rel := &Relationship{
		RelationshipID: relID,
		SessionID:      "session_123",
		Namespace:      "accounting",
		FromFactID:     fromID,
		ToFactID:       toID,
		RelationType:   "ISSUED_BY",
		Weight:         1.0,
		CreatedAt:      now,
	}

	if rel.RelationshipID != relID {
		t.Fatalf("expected relID %v, got %v", relID, rel.RelationshipID)
	}
	if rel.RelationType != "ISSUED_BY" {
		t.Fatalf("expected ISSUED_BY, got %s", rel.RelationType)
	}
}

func TestGraphNeighbor_Direction(t *testing.T) {
	gn := GraphNeighbor{
		Direction: "OUTBOUND",
	}

	if gn.Direction != "OUTBOUND" {
		t.Fatalf("expected OUTBOUND, got %s", gn.Direction)
	}
}
