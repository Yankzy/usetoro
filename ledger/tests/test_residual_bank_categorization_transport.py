import json
import os
import pytest

from bookkeeping_state.bank_categorization.transport_models import (
    BANK_CATEGORIZATION_DAG_ID,
    BANK_CATEGORIZE_SCHEMA_VERSION,
    BankCategorizeItemPayload,
    BankCategorizeRequestEnvelope,
    BankCategorizeResponseEnvelope,
    BankOutcomePayload,
    compute_canonical_bank_payload_digest,
)

@pytest.fixture
def golden_vector_path() -> str:
    return os.path.join(
        os.path.dirname(__file__), "fixtures", "bank_categorize_golden_vector.json"
    )


@pytest.fixture
def golden_vector(golden_vector_path: str) -> dict:
    with open(golden_vector_path, "r", encoding="utf-8") as f:
        return json.load(f)


class TestResidualBankCategorizationTransport:
    def test_a_request_round_trip(self, golden_vector):
        req_data = golden_vector["request"]
        env = BankCategorizeRequestEnvelope.from_dict(req_data)
        assert env.schema_version == BANK_CATEGORIZE_SCHEMA_VERSION
        assert env.request_id == "bank-req:sess-2026-09-16-001:2"
        assert len(env.bank_items) == 2
        
        # Test serialization matching original structure
        exported = env.to_dict()
        assert exported["schema_version"] == req_data["schema_version"]
        assert exported["request_id"] == req_data["request_id"]
        assert len(exported["bank_items"]) == 2

    def test_b_response_classified_round_trip(self):
        outcome = BankOutcomePayload(
            bank_item_id="staged:123",
            status="CLASSIFIED",
            account_code="6064",
            confidence=0.95,
        )
        assert outcome.status == "CLASSIFIED"
        assert outcome.account_code == "6064"
        
        data = outcome.to_dict()
        parsed = BankOutcomePayload.from_dict(data)
        assert parsed == outcome

        # CLASSIFIED without account code
        with pytest.raises(ValueError, match="CLASSIFIED outcome requires account_code"):
            BankOutcomePayload(bank_item_id="staged:123", status="CLASSIFIED")

    def test_c_response_hold_round_trip(self):
        outcome = BankOutcomePayload(
            bank_item_id="staged:456",
            status="HOLD",
            hold_reason="AMBIGUOUS",
        )
        assert outcome.status == "HOLD"
        assert outcome.hold_reason == "AMBIGUOUS"
        assert outcome.account_code is None
        
        data = outcome.to_dict()
        parsed = BankOutcomePayload.from_dict(data)
        assert parsed == outcome

        # HOLD without hold_reason
        with pytest.raises(ValueError, match="HOLD outcome requires hold_reason"):
            BankOutcomePayload(bank_item_id="staged:456", status="HOLD")
            
        # HOLD with account_code
        with pytest.raises(ValueError, match="HOLD outcome must not specify account_code"):
            BankOutcomePayload(bank_item_id="staged:456", status="HOLD", hold_reason="AMBIGUOUS", account_code="6064")

    def test_d_invalid_schema_rejected(self, golden_vector):
        req_data = golden_vector["request"]
        req_data["schema_version"] = "bookkeeping.ase.book_categorize.v1"
        with pytest.raises(ValueError, match="Unsupported schema_version"):
            BankCategorizeRequestEnvelope.from_dict(req_data)

    def test_e_duplicate_bank_item_id_rejected(self, golden_vector):
        req_data = golden_vector["request"]
        # Duplicate the first item
        req_data["bank_items"].append(req_data["bank_items"][0])
        with pytest.raises(ValueError, match="Duplicate bank_item_id"):
            BankCategorizeRequestEnvelope.from_dict(req_data)

    def test_f_invalid_residual_amounts_rejected(self, golden_vector):
        item_data = golden_vector["request"]["bank_items"][0]
        
        # Zero residual
        item_data["residual_amount_units"] = 0
        with pytest.raises(ValueError):
            BankCategorizeItemPayload.from_dict(item_data)
            
        # Residual > original
        item_data["residual_amount_units"] = 500000
        item_data["original_amount_units"] = 450000
        with pytest.raises(ValueError, match="exceeds original amount"):
            BankCategorizeItemPayload.from_dict(item_data)
            
        # Non-staged ID
        item_data["residual_amount_units"] = 450000
        item_data["bank_item_id"] = "item-001"
        with pytest.raises(ValueError, match="requires authoritative 'staged:<uuid>' ID"):
            BankCategorizeItemPayload.from_dict(item_data)
            
        # Invalid currency
        item_data["bank_item_id"] = "staged:123"
        item_data["currency"] = "M"
        with pytest.raises(ValueError):
            BankCategorizeItemPayload.from_dict(item_data)
            
        # Invalid direction
        item_data["currency"] = "MAD"
        item_data["direction"] = "UNKNOWN"
        with pytest.raises(ValueError, match="Invalid direction"):
            BankCategorizeItemPayload.from_dict(item_data)

    def test_g_no_truth_eval_simulator_fields(self, golden_vector):
        item_data = golden_vector["request"]["bank_items"][0]
        
        # extra=forbid will reject random fields
        item_data["some_random_field"] = 123
        with pytest.raises(ValueError):
            BankCategorizeItemPayload.from_dict(item_data)
        
        del item_data["some_random_field"]
        item_data["ground_truth"] = "6064"
        with pytest.raises(ValueError, match="Forbidden field"):
            BankCategorizeItemPayload.from_dict(item_data)
            
        del item_data["ground_truth"]
        item_data["source_artifact_kind"] = "BILL"
        with pytest.raises(ValueError, match="Forbidden field"):
            BankCategorizeItemPayload.from_dict(item_data)

    def test_h_digest_sensitivity(self, golden_vector):
        req_data = golden_vector["request"]
        env = BankCategorizeRequestEnvelope.from_dict(req_data)
        
        base_digest = compute_canonical_bank_payload_digest(
            schema_version=env.schema_version,
            company_id=env.company_id,
            session_id=env.session_id,
            state_revision=env.state_revision,
            persistence_revision=env.persistence_revision,
            dag_id=env.dag_id,
            bank_items=env.bank_items,
        )
        
        # Verify it matches the golden vector!
        assert base_digest == golden_vector["expected_sha256_digest"]
        
        # Change residual amount
        item = env.bank_items[0]
        changed_residual = item.model_copy(update={"residual_amount_units": item.residual_amount_units - 1})
        items_changed_residual = (changed_residual, env.bank_items[1])
        digest_residual = compute_canonical_bank_payload_digest(
            schema_version=env.schema_version,
            company_id=env.company_id,
            session_id=env.session_id,
            state_revision=env.state_revision,
            persistence_revision=env.persistence_revision,
            dag_id=env.dag_id,
            bank_items=items_changed_residual,
        )
        assert digest_residual != base_digest
        
        # Change bank account id
        changed_bank_acc = item.model_copy(update={"bank_account_id": "bank-other"})
        items_changed_acc = (changed_bank_acc, env.bank_items[1])
        digest_acc = compute_canonical_bank_payload_digest(
            schema_version=env.schema_version,
            company_id=env.company_id,
            session_id=env.session_id,
            state_revision=env.state_revision,
            persistence_revision=env.persistence_revision,
            dag_id=env.dag_id,
            bank_items=items_changed_acc,
        )
        assert digest_acc != base_digest
        
        # Change description
        changed_desc = item.model_copy(update={"description": "different"})
        items_changed_desc = (changed_desc, env.bank_items[1])
        digest_desc = compute_canonical_bank_payload_digest(
            schema_version=env.schema_version,
            company_id=env.company_id,
            session_id=env.session_id,
            state_revision=env.state_revision,
            persistence_revision=env.persistence_revision,
            dag_id=env.dag_id,
            bank_items=items_changed_desc,
        )
        assert digest_desc != base_digest
        
        # Verify request_id and requested_at are NOT in digest and changing them doesn't affect canonical output
        # (They are not passed to compute_canonical_bank_payload_digest at all!)
