package agents

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
)

func main() {
	client := openai.NewClient()
	ctx := context.Background()

	question := "What is the weather in New York City?"
	print("> ")
	println(question)

	// 1. Initial request with tools defined
	params := responses.ResponseNewParams{
		Model: openai.ChatModelGPT5_4Nano,
		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String(question),
		},
		Tools: []responses.ToolUnionParam{
			{
				OfFunction: &responses.FunctionToolParam{
					Name:        "get_weather",
					Description: openai.String("Get weather at the given location"),
					Parameters: openai.FunctionParameters{
						"type": "object",
						"properties": map[string]any{
							"location": map[string]string{
								"type": "string",
							},
						},
						"required": []string{"location"},
					},
				},
			},
		},
	}

	// Make the initial Responses API request
	resp, err := client.Responses.New(ctx, params)
	if err != nil {
		panic(err)
	}

	var toolOutputs []responses.ResponseInputItemUnionParam
	var hasToolCall bool

	// 2. Iterate through the output items to find function calls
	for _, item := range resp.Output {
		if item.Type == "function_call" {
			hasToolCall = true
			toolCall := item.AsFunctionCall()

			// Extract the location argument
			var args map[string]any
			if err := json.Unmarshal([]byte(toolCall.Arguments), &args); err != nil {
				panic(err)
			}
			location := args["location"].(string)

			// Simulate executing the local function
			weatherData := getWeather(location)
			fmt.Printf("Weather in %s: %s\n", location, weatherData)

			// Package the local execution output for the follow-up request
			toolOutputs = append(toolOutputs, responses.ResponseInputItemParamOfFunctionCallOutput(toolCall.ID, weatherData))
		}
	}

	// Return early if there were no function calls
	if !hasToolCall {
		fmt.Printf("No function call\n")
		println(resp.OutputText())
		return
	}

	// 3. Follow up using PreviousResponseID and pass the tool outputs
	followUpParams := responses.ResponseNewParams{
		Model:              openai.ChatModelGPT4o,
		PreviousResponseID: openai.String(resp.ID),
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: toolOutputs,
		},
	}

	finalResp, err := client.Responses.New(ctx, followUpParams)
	if err != nil {
		panic(err)
	}

	println(finalResp.OutputText())
}

// Mock function to simulate weather data retrieval
func getWeather(location string) string {
	return "Sunny, 25°C" // In a real app, this calls a weather API
}
