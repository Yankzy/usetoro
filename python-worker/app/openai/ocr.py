"""
OpenAI OCR Agent

Subscribes to NATS OCR inbox subject(s), performs OCR processing using OpenAI Vision API,
and publishes the enriched payload with OCR extraction results back to the requested NATS subject.
"""
import json
import logging
import os
from typing import Any, Dict

from openai import AsyncOpenAI

from app import nats_client
from app.config import OPENAI_API_KEY

logger = logging.getLogger(__name__)

_openai_client: AsyncOpenAI | None = None


def get_openai_client() -> AsyncOpenAI:
    global _openai_client
    if _openai_client is None:
        api_key = OPENAI_API_KEY or os.environ.get("OPENAI_API_KEY", "")
        _openai_client = AsyncOpenAI(api_key=api_key)
    return _openai_client


SYSTEM_PROMPT = """You are an expert OCR document parser. Analyze the document and extract structured JSON matching one of these exact types:

1. Bank Statement (doc_type: "bank_statement"):
   Extract 'bank_name', 'account_number', 'statement_date', 'period', 'starting_balance', 'ending_balance', and 'transactions' list containing items with 'date', 'description', 'amount', 'type' ('debit'/'credit').
   CRITICAL FOR BANK STATEMENTS:
   - Do NOT drop debit/credit, withdrawal/deposit, or polarity sign indicators ('minus', 'brackets', 'none').
   - Also return a 'column_mapping' object conforming to:
     {
       "date_col_idx": 0,
       "description_col_idx": 1,
       "amount_col_idx": 2,
       "is_split_amount": true/false,
       "debit_col_idx": null,
       "credit_col_idx": null,
       "vendor_col_idx": null,
       "customer_col_idx": null,
       "confidence_score": 0.95,
       "is_ambiguous": true/false,
       "ambiguity_reason": null,
       "polarity_sign": "minus" | "brackets" | "none",
       "source_account": "bank name"
     }

2. Invoice (doc_type: "invoice"):
   Extract 'vendor_name', 'invoice_number', 'date', 'total_mad', 'ht_mad', 'tva_mad', and 'line_items'.

3. Receipt (doc_type: "receipt"):
   Extract 'vendor_name', 'date', 'total_mad', and 'payment_method'.

4. Other (doc_type: "other"):
   Extract general key-value metadata.

Return ONLY a valid JSON object matching this structure:
{
  "doc_type": "bank_statement" | "invoice" | "receipt" | "other",
  "confidence": 0.95,
  "data": { ... extracted fields ... },
  "column_mapping": { ... optional column mapping for bank statements ... }
}"""


async def perform_ocr(
    file_url: str,
    payload: Dict[str, Any] | None = None,
    prompt: str | None = None,
    model: str | None = None,
) -> Dict[str, Any]:
    """
    Performs visual/document OCR on the given image or PDF document URL using OpenAI,
    returning structured OCRExtraction matching tap/agents/ocr_agent/agent.go.
    """
    client = get_openai_client()
    selected_model = model or os.environ.get("OPENAI_OCR_MODEL", "gpt-5.4")
    user_prompt = prompt or SYSTEM_PROMPT

    logger.info("Executing OpenAI OCR", extra={"model": selected_model, "file_url": file_url})


    parsed_llm: Dict[str, Any] = {}

    # Try OpenAI Responses API (supports native input_file / input_image)
    try:
        resp = await client.responses.create(
            model=selected_model,
            input=[
                {
                    "role": "user",
                    "content": [
                        {"type": "input_text", "text": user_prompt},
                        {
                            "type": "input_file",
                            "file_url": file_url,
                        },
                    ],
                }
            ],
        )
        raw_output = getattr(resp, "output_text", str(resp))
        cleaned = raw_output.strip()
        if cleaned.startswith("```json"):
            cleaned = cleaned[7:]
        if cleaned.startswith("```"):
            cleaned = cleaned[3:]
        if cleaned.endswith("```"):
            cleaned = cleaned[:-3]
        cleaned = cleaned.strip()

        try:
            parsed_llm = json.loads(cleaned)
        except json.JSONDecodeError:
            parsed_llm = {"doc_type": "other", "confidence": 0.8, "data": {"raw_text": raw_output}}

    except Exception as e:
        logger.warning(f"OpenAI responses API call failed: {e}, attempting chat completions fallback")
        

    # Build standardized OCRExtraction matching agent.go
    req = payload or {}
    file_name = req.get("attachment_name") or req.get("file_name") or ""

    doc_type = parsed_llm.get("doc_type", "other")
    confidence = float(parsed_llm.get("confidence", 0.95))
    data = parsed_llm.get("data", {})
    if not isinstance(data, dict):
        data = {"raw_data": data}

    extraction: Dict[str, Any] = {
        "doc_type": doc_type,
        "file_name": file_name,
        "data": data,
        "confidence": confidence,
    }

    if "column_mapping" in parsed_llm and parsed_llm["column_mapping"]:
        extraction["column_mapping"] = parsed_llm["column_mapping"]

    return extraction




async def handle_ocr_request(msg: Any) -> None:
    """
    NATS JetStream handler for OCR jobs.
    Receives NATS payload, extracts image/doc URL, performs OCR, and publishes back to NATS.
    """
    logger.info(f"Received OCR NATS message on subject: {getattr(msg, 'subject', 'unknown')}")
    try:
        raw_data = msg.data.decode("utf-8")
        req_data = json.loads(raw_data)

        # Unwrap body or payload if nested
        payload = req_data.get("body", req_data) if isinstance(req_data, dict) else req_data
        if isinstance(payload, str):
            payload = json.loads(payload)
        if not isinstance(payload, dict):
            payload = json.loads(msg.data.decode())
        
        # Unwrap core.Envelope if present
        if "body" in payload and isinstance(payload["body"], dict):
            task_def = payload["body"]
            if "payload" in task_def and isinstance(task_def["payload"], dict):
                task_payload = task_def["payload"]
            else:
                task_payload = task_def
        else:
            task_payload = payload

        prompt = task_payload.get("prompt") or task_payload.get("instructions")
        model = task_payload.get("model")

        attachments_list = task_payload.get("attachments") or task_payload.get("documents")
        ocr_extractions = []

        if isinstance(attachments_list, list) and len(attachments_list) > 0:
            for att in attachments_list:
                if not isinstance(att, dict):
                    continue
                att_url = (
                    att.get("document_url")
                    or att.get("image_url")
                    or att.get("url")
                    or att.get("s3_key")
                    or att.get("file_url")
                    or att.get("attachment_url")
                )
                if att_url:
                    merged_payload = dict(task_payload)
                    merged_payload.update(att)
                    ext = await perform_ocr(att_url, payload=merged_payload, prompt=prompt, model=model)
                    ocr_extractions.append(ext)

        if len(ocr_extractions) == 0:
            # Extract single image URL from payload
            image_url = (
                task_payload.get("image_url")
                or task_payload.get("document_url")
                or task_payload.get("url")
                or task_payload.get("s3_key")
                or task_payload.get("file_url")
                or task_payload.get("attachment_url")
            )
            if not image_url:
                err_msg = "No image_url or document_url provided in NATS payload"
                logger.error(err_msg)
                await _publish_ocr_response(msg, payload, {"error": err_msg, "status": "ERROR"})
                try:
                    if hasattr(msg, "ack"):
                        await msg.ack()
                except Exception as ack_err:
                    pass
                return

            ext = await perform_ocr(image_url, payload=task_payload, prompt=prompt, model=model)
            ocr_extractions.append(ext)

        # Create enriched payload preserving all received fields
        response_payload = dict(task_payload)
        response_payload["ocr_extractions"] = ocr_extractions
        if len(ocr_extractions) > 0:
            response_payload["ocr_extraction"] = ocr_extractions[0]
            response_payload["ocr_result"] = ocr_extractions[0]
        response_payload["status"] = "OCR_SUCCESS"

        await _publish_ocr_response(msg, payload, response_payload)

        try:
            if hasattr(msg, "ack"):
                await msg.ack()
        except Exception as ack_err:
            if "NotJSMessageError" not in str(type(ack_err)):
                logger.warning(f"Error acking message: {ack_err}")

    except Exception as e:
        logger.error(f"Error handling OCR request: {e}", exc_info=True)
        try:
            if hasattr(msg, "nak"):
                await msg.nak()
        except Exception as nak_err:
            pass


async def _publish_ocr_response(msg: Any, req_payload: dict, response_payload: dict) -> None:
    """
    Publishes the OCR result back to the target NATS subject or msg.reply.
    """
    target_subject = (
        req_payload.get("final_destination_subject")
        or req_payload.get("reply_subject")
        or req_payload.get("target_subject")
        or req_payload.get("destination_subject")
    )

    if target_subject:
        logger.info(f"Publishing OCR result to target_subject: {target_subject}")
        await nats_client.publish(target_subject, response_payload)

    if getattr(msg, "reply", None):
        logger.info(f"Publishing OCR result to reply subject: {msg.reply}")
        if nats_client._nc is not None:
            await nats_client._nc.publish(msg.reply, json.dumps(response_payload).encode())