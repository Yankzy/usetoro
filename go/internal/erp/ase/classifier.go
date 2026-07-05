package ase

// Classifier represents a domain-specific machine learning or heuristic classifier.
type Classifier interface {
	BuildGenericThinkFunc(promptKey string) ThinkFunc
	BuildDynamicThinkFunc(provider string) ThinkFunc
	BuildPayloadRouterThinkFunc(payloadKey string) ThinkFunc
}
