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
	REQUEST Performative = "request" // Do this specific thing now
	REFUSE  Performative = "refuse"  // I cannot/will not do it
	FAILURE Performative = "failure" // Something went wrong
)
