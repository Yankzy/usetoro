package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUUIDMapper_ObfuscateAndRestore(t *testing.T) {
	m := NewUUIDMapper()

	originalUUID1 := "550e8400-e29b-41d4-a716-446655440000"
	originalUUID2 := "330e8400-e29b-41d4-a716-446655440000"
	originalRealmID := "9341456276406470"

	input := "Alert: Tx " + originalUUID1 + " for Client Cho (Realm: " + originalRealmID + ") under Tenant " + originalUUID2 + " is stuck."

	// Obfuscate
	obfuscated := m.Obfuscate(input)
	assert.Contains(t, obfuscated, "ref_1")
	assert.Contains(t, obfuscated, "ref_2")
	assert.Contains(t, obfuscated, "ref_3")
	assert.NotContains(t, obfuscated, originalUUID1)
	assert.NotContains(t, obfuscated, originalUUID2)
	assert.NotContains(t, obfuscated, originalRealmID)

	// Restore
	restored := m.Restore(obfuscated)
	assert.Equal(t, input, restored)
}

func TestUUIDMapper_Obfuscate_ReusesMapping(t *testing.T) {
	m := NewUUIDMapper()

	originalUUID := "550e8400-e29b-41d4-a716-446655440000"
	originalRealmID := "9341456276406470"

	input1 := "UUID: " + originalUUID + ", Realm: " + originalRealmID
	input2 := "Same UUID: " + originalUUID + ", Same Realm: " + originalRealmID

	obfuscated1 := m.Obfuscate(input1)
	obfuscated2 := m.Obfuscate(input2)

	assert.Contains(t, obfuscated1, "ref_1")
	assert.Contains(t, obfuscated1, "ref_2")

	assert.Contains(t, obfuscated2, "ref_1")
	assert.Contains(t, obfuscated2, "ref_2")
}
