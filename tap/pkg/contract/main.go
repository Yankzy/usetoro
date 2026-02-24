package contract

import (
	"context"
	"fmt"
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

// Lock validates both party signatures and transitions a contract to LOCKED.
func (m *Machine) Lock(ctx context.Context, req *core.Contract) (*core.Contract, error) {
	log.Printf("⚙️ Engine: Attempting to LOCK contract %s", req.ID)

	if !m.verifySig(req.InitiatorDID, req.TermsHash, req.Signatures[req.InitiatorDID]) {
		log.Printf("❌ Invalid Initiator Signature for %s", req.ID)
		return nil, identity.ErrInvalidSignature
	}

	if !m.verifySig(req.AcceptorDID, req.TermsHash, req.Signatures[req.AcceptorDID]) {
		log.Printf("❌ Invalid Acceptor Signature for %s", req.ID)
		return nil, identity.ErrInvalidSignature
	}

	req.Status = core.ContractLocked
	req.CreatedAt = time.Now().UTC()

	if err := m.repo.SaveContract(ctx, req); err != nil {
		return nil, err
	}
	return req, nil
}

// Settle validates the proof's type and worker signature, then transitions to SETTLED.
func (m *Machine) Settle(ctx context.Context, proof *core.Proof) (bool, *core.Contract, error) {
	contract, err := m.repo.GetContract(ctx, proof.TaskID)
	if err != nil {
		return false, nil, err
	}

	if contract.Status != core.ContractLocked {
		return false, contract, nil
	}

	// Validate proof type.
	switch proof.Type {
	case core.ProofGPS, core.ProofClassification, core.ProofAPI:
	default:
		return false, contract, nil
	}

	// Verify the proof signature: the worker (AcceptorDID) must have signed the data.
	if proof.Signature == "" {
		return false, contract, fmt.Errorf("missing proof signature")
	}
	if !m.verifySig(contract.AcceptorDID, string(proof.Data), proof.Signature) {
		return false, contract, fmt.Errorf("invalid proof signature from acceptor %s", contract.AcceptorDID)
	}

	if err := m.repo.UpdateContractStatus(ctx, contract.ID, core.ContractSettled); err != nil {
		return false, nil, err
	}

	contract.Status = core.ContractSettled
	return true, contract, nil
}

func (m *Machine) verifySig(did, data, sig string) bool {
	pubKeyHex, err := identity.PubKeyFromDID(did)
	if err != nil {
		return false
	}
	ok, err := identity.Verify(pubKeyHex, []byte(data), sig)
	return err == nil && ok
}
