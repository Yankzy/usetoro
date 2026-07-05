"""
Marketing NATS Handlers

This module contains the NATS JetStream event handlers for the marketing pipeline.
It acts as the communication bridge between the Go ASE orchestration engine and 
the Python worker's internal logic.
"""
import json
import logging
from typing import Any

from app import nats_client
from app.marketing.ingestion import parse_targets
from app.marketing.permutation import (
    generate_permutations,
    normalize_prospect_name,
    check_domain_catchall,
    check_email_deliverability
)

logger = logging.getLogger(__name__)

async def _publish_reply(msg: Any, proof: dict):
    if msg.reply:
        await nats_client._nc.publish(msg.reply, json.dumps(proof).encode())
    if hasattr(msg, 'ack'):
        await msg.ack()

async def _publish_error(msg: Any, error_msg: str):
    logger.error(error_msg)
    if msg.reply:
        error_proof = {"status": "ERROR", "message": error_msg}
        await nats_client._nc.publish(msg.reply, json.dumps(error_proof).encode())
    if hasattr(msg, 'nak'):
        await msg.nak()

async def handle_ingest_request(msg: Any):
    """
    Handles the 'worker.marketing.ingest' NATS request.
    """
    try:
        targets = parse_targets()
        proof = {"status": "INGEST_SUCCESS", "data": targets}
        await _publish_reply(msg, proof)
    except Exception as e:
        await _publish_error(msg, f"Error in ingest handler: {e}")

async def handle_analyze_request(msg: Any):
    """
    Handles the 'worker.marketing.analyze' NATS request.
    Normalizes prospect name and checks for catch-all domain.
    """
    try:
        req_data = json.loads(msg.data.decode())
        prospect = req_data.get("body", req_data) if isinstance(req_data, dict) else req_data
        if isinstance(prospect, str):
            prospect = json.loads(prospect)
            
        first_name = prospect.get("first_name", "")
        last_name = prospect.get("last_name", "")
        domain = prospect.get("domain", "")
        
        # Normalize
        if first_name:
            prospect["first_name"] = normalize_prospect_name(first_name)
        if last_name:
            prospect["last_name"] = normalize_prospect_name(last_name)
            
        # Catchall Check
        if domain and check_domain_catchall(domain):
            proof = {"status": "IS_CATCHALL", "data": prospect}
        else:
            proof = {"status": "ANALYSIS_SUCCESS", "data": prospect}
            
        await _publish_reply(msg, proof)
    except Exception as e:
        await _publish_error(msg, f"Error in analyze handler: {e}")

async def handle_discover_request(msg: Any):
    """
    Handles the 'worker.marketing.discover' NATS request.
    Runs email discovery using mailscout.
    """
    try:
        req_data = json.loads(msg.data.decode())
        prospect = req_data.get("body", req_data) if isinstance(req_data, dict) else req_data
        if isinstance(prospect, str):
            prospect = json.loads(prospect)
            
        result = generate_permutations(prospect)
        
        if result.get("permutations"):
            proof = {"status": "DISCOVERY_SUCCESS", "data": result}
        else:
            proof = {"status": "DISCOVERY_FAILED", "data": result}
            
        await _publish_reply(msg, proof)
    except Exception as e:
        await _publish_error(msg, f"Error in discover handler: {e}")

async def handle_verify_request(msg: Any):
    """
    Handles the 'worker.marketing.verify' NATS request.
    Verifies if a previously discovered email is still deliverable.
    """
    try:
        req_data = json.loads(msg.data.decode())
        prospect = req_data.get("body", req_data) if isinstance(req_data, dict) else req_data
        if isinstance(prospect, str):
            prospect = json.loads(prospect)
            
        # Assume permutations array has the valid email as the first item
        emails = prospect.get("permutations", [])
        if not emails:
            await _publish_reply(msg, {"status": "BOUNCED", "data": prospect})
            return
            
        email_to_check = emails[0]
        if check_email_deliverability(email_to_check):
            proof = {"status": "STILL_VALID", "data": prospect}
        else:
            proof = {"status": "BOUNCED", "data": prospect}
            
        await _publish_reply(msg, proof)
    except Exception as e:
        await _publish_error(msg, f"Error in verify handler: {e}")

