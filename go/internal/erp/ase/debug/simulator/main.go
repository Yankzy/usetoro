package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Yankzy/usetoro/internal/erp/ase"
)

// run from project root:
// go run internal/erp/ase/debug/simulator/main.go

// yamlToJSON helps parse YAML into JSON-tagged structs by converting it via JSON bytes
func yamlToJSON(b []byte) ([]byte, error) {
	var body interface{}
	if err := yaml.Unmarshal(b, &body); err != nil {
		return nil, err
	}
	body = convertYAMLMapKey(body)
	return json.Marshal(body)
}

func convertYAMLMapKey(i interface{}) interface{} {
	switch x := i.(type) {
	case map[interface{}]interface{}:
		m2 := map[string]interface{}{}
		for k, v := range x {
			m2[k.(string)] = convertYAMLMapKey(v)
		}
		return m2
	case map[string]interface{}:
		m2 := map[string]interface{}{}
		for k, v := range x {
			m2[k] = convertYAMLMapKey(v)
		}
		return m2
	case []interface{}:
		for i, v := range x {
			x[i] = convertYAMLMapKey(v)
		}
	}
	return i
}

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "", "Path to the ASE DAG YAML configuration file")
	flag.Parse()

	if configPath == "" {
		// Provide a helpful default if run from various directories
		if _, err := os.Stat("go/internal/erp/ase/dags/ase_gaap_us.yml"); err == nil {
			configPath = "go/internal/erp/ase/dags/ase_gaap_us.yml"
		} else if _, err := os.Stat("internal/erp/ase/dags/ase_gaap_us.yml"); err == nil {
			configPath = "internal/erp/ase/dags/ase_gaap_us.yml"
		} else if _, err := os.Stat("../../dags/ase_gaap_us.yml"); err == nil {
			configPath = "../../dags/ase_gaap_us.yml"
		} else {
			fmt.Println("Usage: go run main.go --config <path_to_yaml>")
			os.Exit(1)
		}
	}

	b, err := os.ReadFile(configPath)
	if err != nil {
		fmt.Printf("❌ Failed to read file: %v\n", err)
		os.Exit(1)
	}

	jsonBytes, err := yamlToJSON(b)
	if err != nil {
		fmt.Printf("❌ Failed to parse YAML: %v\n", err)
		os.Exit(1)
	}

	var cfg ase.ASEConfig
	if err := json.Unmarshal(jsonBytes, &cfg); err != nil {
		fmt.Printf("❌ Failed to unmarshal to ASEConfig: %v\n", err)
		os.Exit(1)
	}

	runSimulation(&cfg)
}

func runSimulation(cfg *ase.ASEConfig) {
	reader := bufio.NewReader(os.Stdin)
	fmt.Println("==================================================")
	fmt.Println("🚀 Starting ASE DAG Interactive Simulator 🚀")
	fmt.Println("==================================================")

	nodes := cfg.DAG.Nodes
	if len(nodes) == 0 {
		fmt.Println("❌ No nodes found in DAG config.")
		return
	}

	for {
		currentNodeID := cfg.DAG.EntryNode
		if currentNodeID == "" {
			if _, ok := nodes["bank"]; ok {
				currentNodeID = "bank"
			} else if _, ok := nodes["root"]; ok {
				currentNodeID = "root"
			} else if _, ok := nodes["generate_email_dag"]; ok {
				currentNodeID = "generate_email_dag"
			} else if _, ok := nodes["triage_failed"]; ok {
				currentNodeID = "triage_failed"
			} else {
				for k := range nodes {
					currentNodeID = k
					break
				}
			}
		}

		fmt.Printf("\n==================================================\n")
		fmt.Printf("--- NEW TRANSACTION SIMULATION ---\n")
		trace := []string{}

		for {
			node, exists := nodes[currentNodeID]
			if !exists {
				fmt.Printf("❌ ERROR: Node '%s' does not exist in configuration!\n", currentNodeID)
				break
			}

			trace = append(trace, currentNodeID)
			fmt.Printf("\n📍 Current Node: %s\n", currentNodeID)
			fmt.Printf("   Kind: %s\n", node.Kind)

			if node.Name != "" && node.Name != currentNodeID {
				fmt.Printf("   Name: %s\n", node.Name)
			}

			if node.Kind == "terminal" {
				closeStatus := node.ExecutionParams["close_status"]
				if closeStatus == "" {
					closeStatus = "CLASSIFIED (default)"
				}
				fmt.Printf("\n🏁 Reached Terminal Node. Final Status: %s\n", closeStatus)
				break
			}

			// Handle Holding Gates
			if node.Kind == "holding_gate" {
				if node.HoldStateSignal != "" {
					fmt.Printf("\n⏸  STATIC HOLD GATE Triggered!\n")
					fmt.Printf("   Reason: %s\n", node.HoldReasonString)
					if node.Email != nil {
						fmt.Printf("   📧 Email Subject: %s\n", node.Email.Subject)
					}
					fmt.Printf("   Resume Child: %s\n", node.ResumeChild)

					fmt.Printf("\nDo you want to simulate Human Approval to resume? (y/n): ")
					ans, _ := reader.ReadString('\n')
					ans = strings.TrimSpace(strings.ToLower(ans))
					if ans == "y" {
						if node.ResumeChild != "" {
							currentNodeID = node.ResumeChild
							fmt.Printf("➡️  Resuming to: %s\n", currentNodeID)
							continue
						} else {
							fmt.Printf("❌ ERROR: No resume_child configured for this hold gate!\n")
							break
						}
					} else {
						fmt.Printf("🛑 Simulation halted at Hold Gate.\n")
						break
					}
				}
			}

			if len(node.Children) == 0 && node.DefaultChild == "" && node.ResumeChild == "" {
				fmt.Println("\n🛑 Dead End Node (No children or resume configured).")
				break
			}

			fmt.Printf("\n🧠 Simulated LLM prompt key: %s\n", node.PromptKey)
			fmt.Println("Options (choose the LLM routing key):")

			var validOptions []string
			optionMap := make(map[string]string)

			for key, child := range node.Children {
				validOptions = append(validOptions, key)
				optionMap[strings.ToLower(key)] = child
				fmt.Printf("  [%d] %s -> (routes to: %s)\n", len(validOptions), key, child)
			}

			validOptions = append(validOptions, "LOW_CONFIDENCE")
			optionMap["low_confidence"] = "HOLD"
			fmt.Printf("  [%d] LOW_CONFIDENCE -> (triggers guardrail hold)\n", len(validOptions))

			if node.DefaultChild != "" {
				validOptions = append(validOptions, "UNKNOWN")
				optionMap["unknown"] = node.DefaultChild
				fmt.Printf("  [%d] UNKNOWN -> (routes to default_child: %s)\n", len(validOptions), node.DefaultChild)
			}

			var choiceStr string
			for {
				fmt.Printf("\nEnter your choice (number or exact name): ")
				choice, err := reader.ReadString('\n')
				if err != nil {
					if err == io.EOF {
						return
					}
					fmt.Println("Error reading input:", err)
					return
				}
				choice = strings.TrimSpace(choice)

				// Check if choice is a number
				var num int
				_, err = fmt.Sscanf(choice, "%d", &num)
				if err == nil && num > 0 && num <= len(validOptions) {
					choiceStr = validOptions[num-1]
					break
				}

				// Check if choice is exact string (case-insensitive)
				found := false
				for _, opt := range validOptions {
					if strings.EqualFold(opt, choice) {
						choiceStr = opt
						found = true
						break
					}
				}
				if found {
					break
				}
				fmt.Printf("⚠️  Invalid choice '%s'.\n", choice)
			}

			if choiceStr == "LOW_CONFIDENCE" {
				fmt.Printf("\n⏸  LOW CONFIDENCE HOLD Triggered!\n")
				fmt.Printf("🛑 Simulation halted.\n")
				break
			}

			childID := optionMap[strings.ToLower(choiceStr)]
			currentNodeID = childID
		}

		fmt.Printf("\n📝 Full Trace Path:\n%s\n", strings.Join(trace, " -> "))
		fmt.Printf("\nDo you want to run another simulation? (y/n): ")
		again, _ := reader.ReadString('\n')
		again = strings.TrimSpace(strings.ToLower(again))
		if again != "y" && again != "yes" && again != "" {
			break
		}
	}
	fmt.Println("Exiting simulator. Goodbye!")
}
