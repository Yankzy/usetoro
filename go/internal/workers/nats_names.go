package workers

import "strings"

// durableFromSubject derives a stable JetStream durable consumer name from a NATS subject.
func durableFromSubject(subject string) string {
	name := sanitizeSubjectName(subject)
	if name == "" {
		return "worker-durable"
	}
	return name + "-durable"
}

// groupFromSubject derives a stable queue group from the subscription subject.
func groupFromSubject(subject string) string {
	name := sanitizeSubjectName(subject)
	if name == "" {
		return "worker-group"
	}
	return name + "-group"
}

func sanitizeSubjectName(subject string) string {
	if strings.TrimSpace(subject) == "" {
		return ""
	}
	replacer := strings.NewReplacer(
		".", "-",
		"*", "any",
		">", "all",
		":", "-",
		"/", "-",
		"\\", "-",
		" ", "-",
	)
	name := replacer.Replace(subject)
	name = strings.Trim(name, "-")
	return name
}
