package contract

import (
	"log"
	"time"

	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/pkg/identity"
	"github.com/Yankzy/usetoro/tap/pkg/store"
)

// Machine orchestrates the state transitions of a contract.
type Machine struct {
	repo store.Repository
}

func NewContract(repo store.Repository) *Machine {
	return &Machine{repo: repo}
}

// Lock validates signatures and transitions a contract to LOCKED.
func (m *Machine) Lock(req *core.Contract) (*core.Contract, error) {
	log.Printf("⚙️ Engine: Attempting to LOCK contract %s", req.ID)

	// 1. Verify Initiator Signature
	if !m.verifySig(req.InitiatorDID, req.TermsHash, req.Signatures[req.InitiatorDID]) {
		log.Printf("❌ Invalid Initiator Signature for %s", req.ID)
		return nil, identity.ErrInvalidSignature // You'd define this error
	}

	// 2. Verify Acceptor Signature
	if !m.verifySig(req.AcceptorDID, req.TermsHash, req.Signatures[req.AcceptorDID]) {
		log.Printf("❌ Invalid Acceptor Signature for %s", req.ID)
		return nil, identity.ErrInvalidSignature
	}

	// 3. Transition State
	req.Status = core.ContractLocked
	req.CreatedAt = time.Now().UTC()

	// 4. Persist
	if err := m.repo.SaveContract(req); err != nil {
		return nil, err
	}

	return req, nil
}

// Settle validates a proof and transitions a contract to SETTLED.
func (m *Machine) Settle(proof *core.Proof) (*core.Contract, bool, error) {
	// 1. Fetch State
	contract, err := m.repo.GetContract(proof.TaskID)
	if err != nil {
		return nil, false, err
	}

	if contract.Status != core.ContractLocked {
		return contract, false, nil // Not ready or already settled
	}

	// 2. Validate Proof (Simplified Logic)
	// In production, this would call specific sub-validators based on contract.Domain
	isValid := false
	switch proof.Type {
	case core.ProofGPS, core.ProofClassification, core.ProofAPI:
		// Trust the signature (assuming we verified sender is a valid Oracle/Validator)
		isValid = true
	}

	if !isValid {
		return contract, false, nil
	}

	// 3. Transition State
	if err := m.repo.UpdateStatus(contract.ID, core.ContractSettled); err != nil {
		return nil, false, err
	}

	contract.Status = core.ContractSettled
	return contract, true, nil
}

// Helper
func (m *Machine) verifySig(did, data, sig string) bool {
	// Stub: In real impl, use identity.Verify(did, []byte(data), sig)
	// Assuming non-empty for MVP flow
	return did != "" && sig != ""
}
