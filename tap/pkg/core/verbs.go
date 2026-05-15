package core

// Performative defines the FIPA-ACL communicative act (The Verb).
type Performative string

const (
	// Negotiation Verbs
	CFP             Performative = "cfp"             // Call For Proposal (I need X)
	PROPOSE         Performative = "propose"         // I can do X for $Y
	ACCEPT_PROPOSAL Performative = "accept-proposal" // Deal
	REJECT_PROPOSAL Performative = "reject-proposal" // No thanks

	// Information Verbs
	INFORM    Performative = "inform"    // Here is the result/info
	QUERY_REF Performative = "query-ref" // Who can do X? (Almanac lookup)

	// Execution Verbs
	REQUEST  Performative = "request"  // Do this specific thing now
	DELEGATE Performative = "delegate" // Dynamically generate and execute a sub-workflow
	REFUSE   Performative = "refuse"   // I cannot/will not do it
	FAILURE  Performative = "failure"  // Something went wrong
)

var validPerformatives = map[Performative]struct{}{
	CFP:             {},
	PROPOSE:         {},
	ACCEPT_PROPOSAL: {},
	REJECT_PROPOSAL: {},
	INFORM:          {},
	QUERY_REF:       {},
	REQUEST:         {},
	DELEGATE:        {},
	REFUSE:          {},
	FAILURE:         {},
}

// IsValidPerformative reports whether p is one of the protocol-defined verbs.
func IsValidPerformative(p Performative) bool {
	_, ok := validPerformatives[p]
	return ok
}
