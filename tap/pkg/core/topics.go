package core

import (
	"fmt"
)

// Standard NATS Subject Prefixes
const (
	PrefixTasks   = "tasks"   // For task routing
	PrefixAgents  = "agents"  // For agent inbox
	PrefixEvents  = "events"  // For event routing
	PrefixAlmanac = "almanac" // For almanac routing
)

// --- Task Routing ---

// BuildTaskSubject constructs the subject for broadcasting new work.
// Format: tasks.<domain>.<complexity>.<task_type>
// Example: tasks.accounting.1.verify
func BuildTaskSubject(domain string, complexity TaskComplexity, taskType string) string {
	return fmt.Sprintf("%s.%s.%d.%s", PrefixTasks, domain, complexity, taskType)
}

// Example: events.accounting.1.verify
func BuildEventSubject(domain string, complexity TaskComplexity, taskType string) string {
	return fmt.Sprintf("%s.%s.%d.%s", PrefixEvents, domain, complexity, taskType)
}

// Example: almanac.accounting.1.verify
func BuildAlmanacSubject(domain string, complexity TaskComplexity, taskType string) string {
	return fmt.Sprintf("%s.%s.%d.%s", PrefixAlmanac, domain, complexity, taskType)
}

// --- Agent Routing ---

// BuildAgentInbox constructs the direct address for a specific Agent.
// Format: agents.did.inbox
func BuildAgentInbox(did string) string {
	return fmt.Sprintf("%s.%s.inbox", PrefixAgents, did)
}

// --- Almanac Routing ---

// SubjectAlmanacQuery is where agents send "Who can do X?" requests
const SubjectAlmanacQuery = "almanac.query"

// SubjectAlmanacRegister is where agents send their heartbeats
const SubjectAlmanacRegister = "almanac.register"
