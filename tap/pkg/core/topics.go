package core

import (
	"fmt"
	"strings"
)

// Standard NATS Subject Prefixes
const (
	PrefixTasks                 = "tasks"   // For task routing
	PrefixAgents                = "agents"  // For agent inbox
	PrefixWorkers               = "worker"  // For worker inbox
	PrefixWorkerActivities      = "workers" // For worker activity_type prefix
	PrefixEvents                = "events"  // For event routing
	PrefixAlmanac               = "almanac" // For almanac routing
	PostmarkInboundEmailSubject = "worker.inbox.email.postmark_inbound"
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
// BuildWorkerInboxFromActivity derives a worker inbox subject from an activity type.
// Example activity_type: workers.database.insert_rows -> worker.inbox.database.insert_rows
func BuildWorkerInboxFromActivity(activityType string) (string, error) {
	parts := strings.Split(activityType, ".")
	if len(parts) < 2 || parts[0] != PrefixWorkerActivities {
		return "", fmt.Errorf("invalid worker activity_type %q", activityType)
	}
	return fmt.Sprintf("%s.inbox.%s", PrefixWorkers, strings.Join(parts[1:], ".")), nil
}

// BuildAgentInbox constructs the direct address for a specific Agent.
// Format: agents.did.inbox
func BuildAgentInbox(did string) string {
	return fmt.Sprintf("%s.%s.inbox", PrefixAgents, did)
}

// BuildWorkerInbox constructs the direct address for a specific Worker.
// Format: worker.inbox.<worker_id>
func BuildWorkerInbox(workerID string) string {
	return fmt.Sprintf("%s.inbox.%s", PrefixWorkers, workerID)
}

// BuildTaskSubjectFromActivity derives a canonical task subject for an agent activity type.
// Example activity_type: agents.accounting.map_csv -> tasks.accounting.1.map_csv
func BuildTaskSubjectFromActivity(activityType string, complexity TaskComplexity) (string, error) {
	parts := strings.Split(activityType, ".")
	if len(parts) < 3 || parts[0] != PrefixAgents {
		return "", fmt.Errorf("invalid agent activity_type %q", activityType)
	}
	normalizedComplexity, err := NormalizeTaskComplexity(complexity)
	if err != nil {
		return "", err
	}
	return BuildTaskSubject(parts[1], normalizedComplexity, strings.Join(parts[2:], ".")), nil
}

// NormalizeTaskQueue ensures queue values are canonical for a given activity type.
// - agents.* : uses explicit queue if provided, otherwise derives from BuildTaskSubjectFromActivity.
// - workers.*: accepts worker id (e.g. csv-mapping-worker) or full worker inbox subject.
func NormalizeTaskQueue(activityType, queue string) (string, error) {
	return NormalizeTaskQueueWithComplexity(activityType, queue, ComplexityEntry)
}

// NormalizeTaskQueueWithComplexity behaves like NormalizeTaskQueue, but allows specifying agent task complexity.
func NormalizeTaskQueueWithComplexity(activityType, queue string, complexity TaskComplexity) (string, error) {
	switch {
	case strings.HasPrefix(activityType, PrefixAgents+"."):
		// For agents we always derive the canonical task subject so complexity-based
		// routing and payments remain consistent, even if a custom queue was provided.
		return BuildTaskSubjectFromActivity(activityType, complexity)
	case strings.HasPrefix(activityType, PrefixWorkerActivities+"."): // workers.*
		// Derive inbox from activity type when not explicitly provided to standardize routing.
		if queue == "" {
			return BuildWorkerInboxFromActivity(activityType)
		}
		// Accept explicit inbox subjects or raw worker IDs.
		if strings.HasPrefix(queue, PrefixWorkers+".inbox.") {
			return queue, nil
		}
		// Allow dotted worker IDs (map to inbox)
		if strings.Contains(queue, ".") {
			return fmt.Sprintf("%s.%s", PrefixWorkers+".inbox", queue), nil
		}
		return BuildWorkerInbox(queue), nil
	default:
		if queue != "" {
			return queue, nil
		}
		return "", fmt.Errorf("unsupported activity_type %q", activityType)
	}
}

// --- Almanac Routing ---

// SubjectAlmanacQuery is where agents send "Who can do X?" requests
const SubjectAlmanacQuery = "almanac.query"

// SubjectAlmanacRegister is where agents send their heartbeats
const SubjectAlmanacRegister = "almanac.register"
