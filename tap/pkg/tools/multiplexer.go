package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
)

// SDKMultiplexer wraps any Go SDK client and exposes its methods as a single LLM tool.
type SDKMultiplexer struct {
	Client interface{}
}

func NewSDKMultiplexer(client interface{}) *SDKMultiplexer {
	return &SDKMultiplexer{Client: client}
}

func (m *SDKMultiplexer) Name() string { return "execute_sdk_method" }
func (m *SDKMultiplexer) Description() string {
	return "Executes a method on the underlying SDK client. Pass the exact method name and a JSON object representing its parameters."
}

func (m *SDKMultiplexer) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"method_name": {
				"type": "string",
				"description": "The exact name of the Go method to invoke on the SDK client."
			},
			"arguments": {
				"type": "object",
				"description": "A JSON object containing the arguments to pass to the method. Keys must match the struct fields or parameter names expected."
			}
		},
		"required": ["method_name"]
	}`)
}

func (m *SDKMultiplexer) Call(ctx context.Context, args map[string]any) (string, error) {
	methodName, ok := args["method_name"].(string)
	if !ok {
		return "", fmt.Errorf("method_name is required and must be a string")
	}

	clientVal := reflect.ValueOf(m.Client)
	methodVal := clientVal.MethodByName(methodName)

	if !methodVal.IsValid() {
		return "", fmt.Errorf("method %s not found on SDK client", methodName)
	}

	methodType := methodVal.Type()
	numIn := methodType.NumIn()

	callArgs := make([]reflect.Value, numIn)

	// Simple heuristic mapping:
	// If the method expects (context.Context, *Params), we build the Params struct.
	// If the method expects (context.Context, BodyRequestBody), we build the body.
	
	rawArgs, ok := args["arguments"].(map[string]any)
	if !ok {
		rawArgs = make(map[string]any)
	}
	
	argsBytes, err := json.Marshal(rawArgs)
	if err != nil {
		return "", fmt.Errorf("failed to marshal arguments: %w", err)
	}

	for i := 0; i < numIn; i++ {
		paramType := methodType.In(i)
		
		// If it's a context, pass the provided context
		if paramType.Implements(reflect.TypeOf((*context.Context)(nil)).Elem()) {
			callArgs[i] = reflect.ValueOf(ctx)
			continue
		}

		// If it's variadic (like reqEditors ...RequestEditorFn), just omit them (pass nil/empty)
		// For simplicity, we create zero values for anything we can't map cleanly yet.
		// A more robust implementation would match map keys to parameter names if possible.
		
		// Let's attempt to unmarshal the entire JSON into the first non-context struct parameter.
		val := reflect.New(paramType)
		// Try to unmarshal. If param is a pointer, unmarshal into val.Interface(). If it's a struct, unmarshal into a pointer to the struct.
		if err := json.Unmarshal(argsBytes, val.Interface()); err == nil {
			if paramType.Kind() == reflect.Ptr {
				callArgs[i] = val
			} else {
				callArgs[i] = val.Elem()
			}
		} else {
			// fallback to zero value
			callArgs[i] = reflect.Zero(paramType)
		}
	}

	results := methodVal.Call(callArgs)
	
	// We expect (*Response, error) or similar
	if len(results) > 0 {
		lastIdx := len(results) - 1
		errVal := results[lastIdx]
		if !errVal.IsNil() {
			if err, ok := errVal.Interface().(error); ok {
				return "", fmt.Errorf("SDK method returned error: %w", err)
			}
		}
		
		// Extract response payload
		if len(results) > 1 {
			resBytes, err := json.Marshal(results[0].Interface())
			if err != nil {
				return fmt.Sprintf("Success, but failed to marshal response: %v", err), nil
			}
			return string(resBytes), nil
		}
		return "Success", nil
	}

	return "Method invoked successfully", nil
}
