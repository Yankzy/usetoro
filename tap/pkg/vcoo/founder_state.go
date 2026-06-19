package vcoo

import (
	"bufio"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type Task struct {
	ID     string  `json:"id"`
	Weight float64 `json:"weight"`
	Status string  `json:"status"` // ACTIVE, BACKLOG, COMPLETED
}

type Target struct {
	ID     string  `json:"id"`
	Metric float64 `json:"metric"`
	Unit   string  `json:"unit"`
}

type FounderState struct {
	Tasks   []Task   `json:"tasks"`
	Targets []Target `json:"targets"`
}

var (
	taskRegex   = regexp.MustCompile(`-\s*Task ID:\s*` + "`" + `([^` + "`" + `]+)` + "`" + `\s*\|\s*Weight:\s*([\d.]+)\s*\|\s*Status:\s*(\w+)`)
	targetRegex = regexp.MustCompile(`-\s*Target ID:\s*` + "`" + `([^` + "`" + `]+)` + "`" + `\s*\|\s*Metric:\s*([\d.]+)\s*\|\s*Unit:\s*(\w+)`)
)

// ParseFounderState parses the Markdown state document (supporting OKF specification)
func ParseFounderState(content string) (*FounderState, error) {
	state := &FounderState{
		Tasks:   make([]Task, 0),
		Targets: make([]Target, 0),
	}

	body := content
	parts := strings.SplitN(content, "---", 3)
	if len(parts) == 3 {
		body = parts[2]
	}

	scanner := bufio.NewScanner(strings.NewReader(body))
	for scanner.Scan() {
		line := scanner.Text()

		if matches := taskRegex.FindStringSubmatch(line); matches != nil {
			weight, _ := strconv.ParseFloat(matches[2], 64)
			state.Tasks = append(state.Tasks, Task{
				ID:     matches[1],
				Weight: weight,
				Status: strings.ToUpper(matches[3]),
			})
		} else if matches := targetRegex.FindStringSubmatch(line); matches != nil {
			metric, _ := strconv.ParseFloat(matches[2], 64)
			state.Targets = append(state.Targets, Target{
				ID:     matches[1],
				Metric: metric,
				Unit:   matches[3],
			})
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return state, nil
}

// FormatFounderState formats the parsed state back to Markdown format in OKF specification
func FormatFounderState(state *FounderState) string {
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString("type: Playbook\n")
	sb.WriteString("title: Founder Playbook\n")
	sb.WriteString("description: Master system targets and engineering milestones\n")
	sb.WriteString("---\n")
	sb.WriteString("# MASTER SYSTEM INTENT: FOUNDER MATRIX v1\n")
	sb.WriteString("## Baseline Constraints: Launch Window June 2026\n\n")
	sb.WriteString("### 1. Mandatory Engineering Milestones\n")
	for _, t := range state.Tasks {
		sb.WriteString(fmt.Sprintf("- Task ID: `%s` | Weight: %.2f | Status: %s\n", t.ID, t.Weight, t.Status))
	}
	sb.WriteString("\n### 2. The 10X Weekly Distribution Benchmarks (Targets)\n")
	for _, t := range state.Targets {
		sb.WriteString(fmt.Sprintf("- Target ID: `%s`  | Metric: %.0f | Unit: %s\n", t.ID, t.Metric, t.Unit))
	}
	return sb.String()
}
