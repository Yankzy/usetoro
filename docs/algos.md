# Algorithmic Optimizations

This document captures potential algorithmic optimization opportunities discovered across the Toro codebase.
They are documented here for future implementation when performance tuning becomes a priority.

## 1. Quickbooks Rule Engine Numeric Set Search 
**Location:** `go/internal/erp/adapters/quickbooks/sdk/rule_engine.go`

### Current State
When the rule engine evaluates the `OpIn` and `OpNotIn` operators against `amount` fields, it parses a JSON array into a slice of `float64` (`compiledNumericSet`). During the evaluation of every transaction, the engine performs an `O(N)` linear scan over this slice within the `numericSetContains` function using an `epsilon` to account for floating-point drift.

```go
func numericSetContains(set []float64, val float64) bool {
	for _, s := range set {
		d := val - s
		if d < numericEpsilon && d > -numericEpsilon {
			return true
		}
	}
	return false
}
```

### Proposed Optimization: Binary Search (`O(log N)`)
Since the compiled rule conditions are evaluated heavily (across many transactions), we can improve the performance by replacing the linear scan with a **Binary Search**.

1. **Compilation Phase:** Sort the dataset exactly once when compiling the rule:
   ```go
   // Inside (*RuleCondition).compile()
   sort.Float64s(c.compiledNumericSet)
   ```

2. **Evaluation Phase:** Use `sort.Search` to quickly locate the target element or the nearest element, keeping the \epsilon boundary checks intact. This changes the evaluation complexity from `O(N)` to `O(log N)`.

   ```go
   func numericSetContains(set []float64, val float64) bool {
       // set is already sorted during compile()
       idx := sort.Search(len(set), func(i int) bool {
           return set[i] >= val-numericEpsilon
       })
       if idx < len(set) {
           d := val - set[idx]
           if d < numericEpsilon && d > -numericEpsilon {
               return true
           }
       }
       return false
   }
   ```

## 2. Redundant O(N log N) Sort in AI Chart of Accounts Mapper
**Location:** `go/internal/services/ai/coa_mapper.go`

### Current State
In `MapDescriptionToAccount`, after querying vector matches from Pinecone (`queryVectors`), the code explicitly re-sorts the results descending by score:
```go
// 4. Sort by score descending (Pinecone usually does this, but we filter so we ensure)
sort.Slice(results, func(i, j int) bool {
    return results[i].Score > results[j].Score
})
```

### Proposed Optimization: Remove `sort.Slice`
Pinecone inherently guarantees that queried vectors are returned in descending order by `Score`. The filtering step in the loop above the sort is an `O(N)` operation that implicitly preserves this established order. 

By removing the `sort.Slice` call, we avoid an unnecessary `O(N log N)` computation overhead on every mapped account description without changing the output order.

## 3. Loop Invariant Code Motion (Hoisting) in Entity Resolver
**Location:** `go/internal/services/ai/entity_resolver.go`

### Current State
Inside the `layer3FuzzyRank` function, the code computes the Levenshtein distance between a target string and a list of Pinecone-returned candidate entities. 
```go
for i := range candidates {
    candidate := &candidates[i] 

    // Normalized Levenshtein (0.0 to 1.0 where 1.0 is exact match)
    dist := fuzzy.LevenshteinDistance(strings.ToLower(target), strings.ToLower(candidate.Name))
    ...
}
```

### Proposed Optimization: String Allocation Hoisting
The `target` string remains constant throughout the loop. By computing `strings.ToLower(target)` repeatedly inside the loop, the Go runtime executes redundant lowercase conversions and memory allocations. 

Hoisting `strings.ToLower(target)` outside the loop reduces the time complexity overhead from `O(K * len(target))` to `O(len(target))` (where `K` is the number of candidates).

```go
lowerTarget := strings.ToLower(target)
for i := range candidates {
    candidate := &candidates[i] 
    dist := fuzzy.LevenshteinDistance(lowerTarget, strings.ToLower(candidate.Name))
    ...
}
```
