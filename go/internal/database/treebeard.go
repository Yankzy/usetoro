package database

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

const (
	StepLength = 4
)

// GenerateNextRootPath calculates the next base-36 treebeard path.
// It queries the DB for the highest existing root path and increments it.
func (q *Queries) GenerateNextRootPath(ctx context.Context) (string, error) {
	maxPath, err := q.GetMaxRootPath(ctx)
	if err != nil {
		// If no root paths exist (no rows), sqlc returns an error.
		// We'll treat any error or empty string as "start from 1"
		return padBase36(1, StepLength), nil
	}

	if maxPath == "" {
		return padBase36(1, StepLength), nil
	}

	// The path should be exactly StepLength characters
	if len(maxPath) != StepLength {
		return "", fmt.Errorf("invalid root path length: %s", maxPath)
	}

	// Parse base 36
	val, err := strconv.ParseInt(maxPath, 36, 64)
	if err != nil {
		return "", fmt.Errorf("failed to parse max path: %w", err)
	}

	return padBase36(val+1, StepLength), nil
}

func padBase36(val int64, length int) string {
	encoded := strings.ToUpper(strconv.FormatInt(val, 36))
	if len(encoded) < length {
		return strings.Repeat("0", length-len(encoded)) + encoded
	}
	return encoded
}
