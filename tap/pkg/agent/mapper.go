package agent

import (
	"fmt"
	"regexp"
	"strings"
)

// uuidRegex matches standard UUID strings safely with word boundaries.
var uuidRegex = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)

// refRegex matches the local references we generate.
var refRegex = regexp.MustCompile(`ref_\d+`)

// UUIDMapper securely hides UUIDs from the LLM to prevent hallucinations, substituting them
// with local reference strings during the session, and restoring them on the way out.
type UUIDMapper struct {
	RefToUUID map[string]string
	UUIDToRef map[string]string
	counter   int
}

// NewUUIDMapper creates a new mapping context.
func NewUUIDMapper() *UUIDMapper {
	return &UUIDMapper{
		RefToUUID: make(map[string]string),
		UUIDToRef: make(map[string]string),
		counter:   1,
	}
}

// Obfuscate finds all UUIDs in the input string and replaces them with a short local reference.
func (m *UUIDMapper) Obfuscate(input string) string {
	return uuidRegex.ReplaceAllStringFunc(input, func(match string) string {
		match = strings.ToLower(match)
		if ref, exists := m.UUIDToRef[match]; exists {
			return ref
		}
		ref := fmt.Sprintf("ref_%d", m.counter)
		m.counter++
		m.UUIDToRef[match] = ref
		m.RefToUUID[ref] = match
		return ref
	})
}

// Restore reverses the obfuscation, replacing local references back with the original UUIDs.
func (m *UUIDMapper) Restore(input string) string {
	return refRegex.ReplaceAllStringFunc(input, func(match string) string {
		if originalUUID, exists := m.RefToUUID[match]; exists {
			return originalUUID
		}
		return match
	})
}
