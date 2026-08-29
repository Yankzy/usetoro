import pytest
from app.pcm_dag_eval.domain.models import MockTransactionScenario
from app.pcm_dag_eval.evaluation.ground_truth import resolve_ground_truth_path

def test_resolve_ground_truth_outflow_hold_ambiguous():
    scenario = MockTransactionScenario(
        transaction_id="tx_123",
        cash_direction="OUTFLOW",
        edge_key="HOLD_AMBIGUOUS_BANK_OUTFLOW",
        raw_description="test",
        raw_amount="100.00",
        currency="MAD",
        parent_node="bank_transaction_classifier_outflow",
        expected_child_node="hold_ambiguous_bank_line",
        counterparty_name="n/a",
        ice_number="n/a",
        notes="",
        payload_modifiers={}
    )
    
    path = resolve_ground_truth_path(scenario)
    assert path.expected_nodes == ["direction_router", "bank_transaction_classifier_outflow", "hold_ambiguous_bank_line"]
    assert path.expected_edges == ["OUTFLOW", "HOLD_AMBIGUOUS_BANK_OUTFLOW"]
    assert path.expected_final_state == "HOLD_AMBIGUOUS_BANK_OUTFLOW"
    
def test_resolve_ground_truth_inflow_happy_path_treasury():
    scenario = MockTransactionScenario(
        transaction_id="tx_456",
        cash_direction="INFLOW",
        edge_key="TRANSIT_TRANSFER_IN_5115",
        raw_description="test",
        raw_amount="200.00",
        currency="MAD",
        parent_node="bank_transaction_classifier_inflow",
        expected_child_node="treasury_reconciler",
        counterparty_name="n/a",
        ice_number="n/a",
        notes="",
        payload_modifiers={}
    )
    
    path = resolve_ground_truth_path(scenario)
    
    # direction -> inflow_classifier -> treasury_reconciler -> compliance_router -> pcge_account_resolver -> journal_builder -> journal_validator -> post_bank_cash_journal -> terminal
    assert path.expected_nodes[0] == "direction_router"
    assert path.expected_nodes[1] == "bank_transaction_classifier_inflow"
    assert path.expected_nodes[2] == "treasury_reconciler"
    assert path.expected_nodes[3] == "compliance_router"
    assert path.expected_nodes[4] == "pcge_account_resolver"
    assert path.expected_nodes[5] == "journal_builder"
    assert path.expected_nodes[6] == "journal_validator"
    assert path.expected_nodes[7] == "post_bank_cash_journal"
    assert path.expected_nodes[8] == "bank_cash_complete"

    assert path.expected_final_state == "READY_FOR_SYNC"
