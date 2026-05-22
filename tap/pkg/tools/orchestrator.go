package tools

import (
	"context"
	"fmt"
	"sync"

	"golang.org/x/sync/errgroup"
)

// concurrentSafeTools is the set of tool names that can run in parallel.
// FileWrite, FileEdit, and Agent must run serially because they mutate state.
var concurrentSafeTools = map[string]bool{
	"Bash":          true,
	"FileRead":      true,
	"Grep":          true,
	"WebFetch":      true,
	"AlmanacLookup": true,
}

// maxConcurrency is the maximum number of tools to run in parallel.
const maxConcurrency = 5

// ExecuteTools runs a batch of tool calls, partitioning into parallel-safe
// and serial-only groups. Returns results keyed by ToolCallID.
func ExecuteTools(ctx context.Context, calls []ToolCall, tools map[string]Tool) ([]ToolResult, error) {
	if len(calls) == 0 {
		return nil, nil
	}

	// Partition into concurrent-safe and serial-only
	var parallel, serial []ToolCall
	for _, tc := range calls {
		if concurrentSafeTools[tc.Name] {
			parallel = append(parallel, tc)
		} else {
			serial = append(serial, tc)
		}
	}

	results := make([]ToolResult, 0, len(calls))

	// Run parallel-safe tools concurrently with a semaphore
	if len(parallel) > 0 {
		parallelResults := make([]ToolResult, len(parallel))
		sem := make(chan struct{}, maxConcurrency)
		var wg sync.WaitGroup

		for i, tc := range parallel {
			wg.Add(1)
			go func(idx int, call ToolCall) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				tool, ok := tools[call.Name]
				if !ok {
					parallelResults[idx] = ToolResult{
						ToolCallID: call.ID,
						Error:      fmt.Sprintf("unknown tool: %s", call.Name),
					}
					return
				}

				content, err := tool.Call(ctx, call.Input)
				if err != nil {
					parallelResults[idx] = ToolResult{
						ToolCallID: call.ID,
						Error:      err.Error(),
					}
					return
				}

				parallelResults[idx] = ToolResult{
					ToolCallID: call.ID,
					Content:    content,
				}
			}(i, tc)
		}
		wg.Wait()
		results = append(results, parallelResults...)
	}

	// Run serial tools sequentially
	for _, tc := range serial {
		tool, ok := tools[tc.Name]
		if !ok {
			results = append(results, ToolResult{
				ToolCallID: tc.ID,
				Error:      fmt.Sprintf("unknown tool: %s", tc.Name),
			})
			continue
		}

		content, err := tool.Call(ctx, tc.Input)
		if err != nil {
			results = append(results, ToolResult{
				ToolCallID: tc.ID,
				Error:      err.Error(),
			})
			continue
		}

		results = append(results, ToolResult{
			ToolCallID: tc.ID,
			Content:    content,
		})
	}

	return results, nil
}

// ExecuteToolsWithErrGroup runs parallel tools via errgroup, canceling all on first error.
// Serial tools still run sequentially afterward unless the context is canceled.
func ExecuteToolsWithErrGroup(ctx context.Context, calls []ToolCall, tools map[string]Tool) ([]ToolResult, error) {
	if len(calls) == 0 {
		return nil, nil
	}

	var parallel, serial []ToolCall
	for _, tc := range calls {
		if concurrentSafeTools[tc.Name] {
			parallel = append(parallel, tc)
		} else {
			serial = append(serial, tc)
		}
	}

	results := make([]ToolResult, 0, len(calls))

	if len(parallel) > 0 {
		parallelResults := make([]ToolResult, len(parallel))
		g, gctx := errgroup.WithContext(ctx)
		g.SetLimit(maxConcurrency)

		for i, tc := range parallel {
			i, tc := i, tc
			g.Go(func() error {
				select {
				case <-gctx.Done():
					return gctx.Err()
				default:
				}

				tool, ok := tools[tc.Name]
				if !ok {
					parallelResults[i] = ToolResult{
						ToolCallID: tc.ID,
						Error:      fmt.Sprintf("unknown tool: %s", tc.Name),
					}
					return nil
				}

				content, err := tool.Call(gctx, tc.Input)
				if err != nil {
					parallelResults[i] = ToolResult{
						ToolCallID: tc.ID,
						Error:      err.Error(),
					}
					return nil // don't cancel siblings on individual tool errors
				}

				parallelResults[i] = ToolResult{
					ToolCallID: tc.ID,
					Content:    content,
				}
				return nil
			})
		}
		_ = g.Wait() // errors are captured per-result, not propagated
		results = append(results, parallelResults...)
	}

	// Run serial tools sequentially
	for _, tc := range serial {
		select {
		case <-ctx.Done():
			results = append(results, ToolResult{
				ToolCallID: tc.ID,
				Error:      ctx.Err().Error(),
			})
			continue
		default:
		}

		tool, ok := tools[tc.Name]
		if !ok {
			results = append(results, ToolResult{
				ToolCallID: tc.ID,
				Error:      fmt.Sprintf("unknown tool: %s", tc.Name),
			})
			continue
		}

		content, err := tool.Call(ctx, tc.Input)
		if err != nil {
			results = append(results, ToolResult{
				ToolCallID: tc.ID,
				Error:      err.Error(),
			})
			continue
		}

		results = append(results, ToolResult{
			ToolCallID: tc.ID,
			Content:    content,
		})
	}

	return results, nil
}
