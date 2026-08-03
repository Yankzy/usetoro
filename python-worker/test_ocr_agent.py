"""
Standalone CLI test script for Python OCR NATS Agent.

Usage:
    python3 test_ocr_agent.py
"""
import asyncio
import json
import os
import sys

import nats


async def main():
    nats_url = os.environ.get("NATS_URL", "nats://localhost:4222")
    sample_url = (
        sys.argv[1]
        if len(sys.argv) > 1
        else "https://voxprofit.s3.us-east-1.amazonaws.com/2b6e9795-52e6-4dce-a113-59500e9ddc27-facture.pdf?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Checksum-Mode=ENABLED&X-Amz-Credential=AKIATK25RS5VOWJTG3QR%2F20260803%2Fus-east-1%2Fs3%2Faws4_request&X-Amz-Date=20260803T115650Z&X-Amz-Expires=86400&X-Amz-SignedHeaders=host&x-id=GetObject&X-Amz-Signature=a4d0835a7675956ad957c2a2c888aebaab8cb39fcdb40f0f7167cda569496429"
    )

    print(f"🔌 Connecting to NATS at {nats_url}...")
    try:
        nc = await nats.connect(servers=[nats_url])
    except Exception as e:
        print(f"❌ Failed to connect to NATS ({e}). Make sure NATS is running!")
        return

    reply_subject = "worker.inbox.ocr_test_reply"
    sub = await nc.subscribe(reply_subject)

    payload = {
        "session_id": "test-session-001",
        "attachment_name": "releve_bancaire.pdf",
        "document_url": sample_url,
        "final_destination_subject": reply_subject,
    }

    target_subject = "worker.inbox.python.ocr"
    print(f"🚀 Publishing OCR task to NATS subject: '{target_subject}'")
    print(f"📄 Document URL: {sample_url}")

    await nc.publish(target_subject, json.dumps(payload).encode())

    print(f"⏳ Waiting up to 60s for response on '{reply_subject}'...")
    try:
        msg = await sub.next_msg(timeout=60)
        resp_payload = json.loads(msg.data.decode())
        print("\n✅ Received OCR Response:")
        print(json.dumps(resp_payload, indent=2))
    except asyncio.TimeoutError:
        print(f"⏱️ Timed out waiting for response on '{reply_subject}'. Is python-worker running?")
    except Exception as e:
        print(f"❌ Error: {e}")
    finally:
        await nc.close()


if __name__ == "__main__":
    asyncio.run(main())
