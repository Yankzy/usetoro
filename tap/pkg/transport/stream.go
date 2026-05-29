package transport

import (
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
)

// InitStreams ensures the necessary JetStream streams exist.
// This makes the system idempotent.
func InitStreams(js nats.JetStreamContext) error {
	// --- CANONICAL MAPPING OF SUBJECTS TO STREAMS ---
	// We map each subject prefix to its canonical stream name.
	// If NATS finds any other stream claiming these, the Janitor will prune it.
	canonicalMap := map[string]string{
		"tasks.>":     "TASKS",
		"proof.>":     "TASKS",
		"contracts.>": "SETTLEMENT",
		"events.>":    "EVENTS",
	}

	// --- THE AGGRESSIVE JANITOR: PRUNING SUBJECT OVERLAPS ---
	streamsChan := js.StreamsInfo()
	for info := range streamsChan {
		if info == nil {
			continue
		}

		// For every subject in this existing stream, check if it "squats" on
		// subjects that now belong to one of our canonical streams.
		shouldPrune := false
		for _, subject := range info.Config.Subjects {
			for subPrefix, standardName := range canonicalMap {
				// Clean prefix for matching (remove .>)
				prefix := strings.TrimSuffix(subPrefix, ".>")
				if strings.HasPrefix(subject, prefix) && info.Config.Name != standardName {
					shouldPrune = true
					break
				}
			}
			if shouldPrune {
				break
			}
		}

		if shouldPrune {
			_ = js.DeleteStream(info.Config.Name)
		}
	}

	// Helper to ensure stream exists or update subjects if needed
	ensureStream := func(cfg *nats.StreamConfig) error {
		info, err := js.StreamInfo(cfg.Name)
		if err != nil {
			if err == nats.ErrStreamNotFound {
				_, err = js.AddStream(cfg)
				return err
			}
			return err
		}

		// Stream exists. Check if subjects need updating.
		needsUpdate := false
		if len(info.Config.Subjects) != len(cfg.Subjects) {
			needsUpdate = true
		} else {
			existingSubjects := make(map[string]bool)
			for _, s := range info.Config.Subjects {
				existingSubjects[s] = true
			}
			for _, s := range cfg.Subjects {
				if !existingSubjects[s] {
					needsUpdate = true
					break
				}
			}
		}

		if needsUpdate {
			// Merge: use existing immutable fields but update subjects
			newCfg := info.Config
			newCfg.Subjects = cfg.Subjects
			_, err = js.UpdateStream(&newCfg)
			return err
		}

		return nil
	}

	// 1. Tasks Stream (Work Queue)
	if err := ensureStream(&nats.StreamConfig{
		Name:      "TASKS",
		Subjects:  []string{"tasks.>", "proof.>"},
		Retention: nats.WorkQueuePolicy,
		Storage:   nats.FileStorage,
	}); err != nil {
		return fmt.Errorf("failed to ensure tasks stream: %w", err)
	}

	// 2. Events Stream (Audit Log)
	if err := ensureStream(&nats.StreamConfig{
		Name:      "EVENTS",
		Subjects:  []string{"events.>"},
		Retention: nats.LimitsPolicy,
		MaxAge:    30 * 24 * time.Hour,
		Storage:   nats.FileStorage,
	}); err != nil {
		return fmt.Errorf("failed to ensure events stream: %w", err)
	}

	// 3. Settlement Stream (Contracts & Ingests)
	// Renamed from CONTRACTS to SETTLEMENT to match defaults.yml
	if err := ensureStream(&nats.StreamConfig{
		Name:     "SETTLEMENT",
		Subjects: []string{"contracts.>", "raw.ingest.>"},
		Storage:  nats.FileStorage,
	}); err != nil {
		return fmt.Errorf("failed to ensure settlement stream: %w", err)
	}

	return nil
}
