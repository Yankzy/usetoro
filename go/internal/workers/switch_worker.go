package workers

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// When the toro.worker.switch queue receives a TaskDefinition:
// It unmarshals the configuration block (the rules) from the Task's metadata.
// It passes the Task.Payload to Switch.
// It replies to the Orchestrator's Inbox with an INFORM envelope containing the wrapped (or unwrapped) JSON.

// SwitchConfig
type SwitchConfig struct {
	NodeMode   string `json:"nodeMode"`   // "sender" or "receiver"
	RouteIndex int    `json:"routeIndex"` // Used if receiver
	Mode       string `json:"mode"`       // "expression" or "rules"
	Output     int    `json:"output"`     // Used if mode == "expression"
	DataType   string `json:"dataType"`   // "boolean", "dateTime", "number", "string"

	// Value1 is a JSON path (e.g. "amount" or "vendor.name")
	Value1Path string `json:"value1Path"`

	Rules          []Rule `json:"rules"`
	FallbackOutput int    `json:"fallbackOutput"` // Default output if no rules match (-1 for none)
}

type Rule struct {
	Operation string      `json:"operation"`
	Value2    interface{} `json:"value2"`
	Output    int         `json:"output"`
}

// Switch processes an array of items
func Switch(payload []byte, config SwitchConfig) ([]byte, error) {
	// Ensure payload is an array
	items := gjson.ParseBytes(payload)
	if !items.IsArray() {
		return nil, fmt.Errorf("payload must be a JSON array")
	}

	var results []string

	// ==========================================
	// RECEIVER MODE
	// ==========================================
	if config.NodeMode == "receiver" {
		items.ForEach(func(_, item gjson.Result) bool {
			route := item.Get("route")
			if route.Exists() && int(route.Int()) == config.RouteIndex {
				// Strip the route attribute to restore original data
				innerData, _ := sjson.Delete(item.Raw, "route")
				results = append(results, innerData)
			}
			return true
		})

		// Rebuild the JSON array
		return []byte("[" + strings.Join(results, ",") + "]"), nil
	}

	// ==========================================
	// SENDER MODE
	// ==========================================
	if config.NodeMode == "sender" {
		items.ForEach(func(_, item gjson.Result) bool {
			var routedOutput int
			matched := false

			if config.Mode == "expression" {
				routedOutput = config.Output
				matched = true
			} else if config.Mode == "rules" {
				value1 := item.Get(config.Value1Path)

				for _, rule := range config.Rules {
					if compare(value1, rule.Value2, config.DataType, rule.Operation) {
						routedOutput = rule.Output
						matched = true
						break
					}
				}

				if !matched && config.FallbackOutput != -1 {
					routedOutput = config.FallbackOutput
					matched = true
				}
			}

			if matched {
				// Inject route into the original item without altering its schema
				wrapped, _ := sjson.Set(item.Raw, "route", routedOutput)
				results = append(results, wrapped)
			}
			return true
		})

		return []byte("[" + strings.Join(results, ",") + "]"), nil
	}

	return nil, fmt.Errorf("invalid nodeMode: %s", config.NodeMode)
}

func compare(value1 gjson.Result, value2 interface{}, dataType, operation string) bool {
	switch dataType {

	case "boolean":
		v1Bool := value1.Bool()
		v2Bool, _ := value2.(bool)
		switch operation {
		case "equal":
			return v1Bool == v2Bool
		case "notEqual":
			return v1Bool != v2Bool
		}

	case "number":
		v1Num := value1.Float()
		var v2Num float64
		// Safely cast value2 to float64
		switch v := value2.(type) {
		case float64:
			v2Num = v
		case int:
			v2Num = float64(v)
		case float32:
			v2Num = float64(v)
		}

		switch operation {
		case "smaller":
			return v1Num < v2Num
		case "smallerEqual":
			return v1Num <= v2Num
		case "equal":
			return v1Num == v2Num
		case "notEqual":
			return v1Num != v2Num
		case "larger":
			return v1Num > v2Num
		case "largerEqual":
			return v1Num >= v2Num
		}

	case "string":
		v1Str := value1.String()
		v2Str := fmt.Sprintf("%v", value2)

		switch operation {
		case "contains":
			return strings.Contains(v1Str, v2Str)
		case "notContains":
			return !strings.Contains(v1Str, v2Str)
		case "endsWith":
			return strings.HasSuffix(v1Str, v2Str)
		case "notEndsWith":
			return !strings.HasSuffix(v1Str, v2Str)
		case "equal":
			return v1Str == v2Str
		case "notEqual":
			return v1Str != v2Str
		case "startsWith":
			return strings.HasPrefix(v1Str, v2Str)
		case "notStartsWith":
			return !strings.HasPrefix(v1Str, v2Str)
		case "regex":
			matched, _ := regexp.MatchString(v2Str, v1Str)
			return matched
		case "notRegex":
			matched, _ := regexp.MatchString(v2Str, v1Str)
			return !matched
		}

	case "dateTime":
		// Attempt to parse value1
		t1 := value1.Time()

		// Attempt to parse value2 (assuming RFC3339 string from config)
		v2Str, _ := value2.(string)
		t2, err := time.Parse(time.RFC3339, v2Str)
		if err != nil {
			return false // Malformed date in config
		}

		switch operation {
		case "after":
			return t1.After(t2)
		case "before":
			return t1.Before(t2)
		}
	}

	return false
}
