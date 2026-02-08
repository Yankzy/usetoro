package tap

import (
	"fmt"
)

// Standard NATS Subject Prefixes
const (
	PrefixTasks   = "tasks"
	PrefixAgents  = "agents"
	PrefixEvents  = "events"
	PrefixAlmanac = "almanac"
)

// --- Task Routing ---

// BuildTaskSubject constructs the subject for broadcasting new work.
// Format: tasks.<domain>.<complexity>.<task_type>
// Example: tasks.accounting.1.verify
func BuildTaskSubject(domain string, complexity TaskComplexity, taskType string) string {
	return fmt.Sprintf("%s.%s.%d.%s", PrefixTasks, domain, complexity, taskType)
}

// --- Agent Routing ---

// BuildAgentInbox constructs the direct address for a specific Agent.
// Format: agents.<did_suffix>.inbox
// We strip the "did:toro:" prefix to keep subjects shorter.
func BuildAgentInbox(did string) string {
	// naive strip, assumes valid DID
	cleanID := did[9:]
	return fmt.Sprintf("%s.%s.inbox", PrefixAgents, cleanID)
}

// --- Almanac Routing ---

// SubjectAlmanacQuery is where agents send "Who can do X?" requests
const SubjectAlmanacQuery = "almanac.query"

// SubjectAlmanacRegister is where agents send their heartbeats
const SubjectAlmanacRegister = "almanac.register"
