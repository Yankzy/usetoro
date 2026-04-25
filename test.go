package main

import (
	"encoding/json"
	"fmt"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

type SQLWorkerPayload struct {
	Query         string `json:"query"`
	ReturnSubject string `json:"return_subject,omitempty"`
}

func main() {
	payload := struct {
		Query         string `json:"query"`
		ReturnSubject string `json:"return_subject"`
	}{
		Query:         "SELECT 1",
		ReturnSubject: "foo",
	}

	data, _ := json.Marshal(payload)
	env, _ := core.NewEnvelope("id", "src", "dst", "cid", core.INFORM, data)
	envBytes, _ := json.Marshal(env)

	fmt.Println(string(envBytes))

	data2 := envBytes
	var env2 core.Envelope
	if err := json.Unmarshal(data2, &env2); err == nil && len(env2.Body) > 0 {
		data2 = env2.Body
	}

	var payload2 SQLWorkerPayload
	if err := core.UnmarshalTaskPayload(data2, &payload2); err != nil {
		fmt.Println("Error:", err)
		return
	}

	fmt.Printf("Success: %+v\n", payload2)
}
