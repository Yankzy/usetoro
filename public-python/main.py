import asyncio
import os
import json
import logging
import tempfile
import subprocess
import time
import uuid
import nats
from nats.aio.client import Client as NATS

logging.basicConfig(level=logging.INFO, format="%(asctime)s [%(levelname)s] %(name)s: %(message)s")
logger = logging.getLogger("public-python")

async def run_script(script_code: str, inputs: dict, tenant_id: str) -> dict:
    # 1. Create a temp file for the script
    with tempfile.NamedTemporaryFile(suffix=".py", mode="w", delete=False) as f:
        f.write(script_code)
        temp_filename = f.name
        
    try:
        # 2. Run the script as a subprocess
        # Pipe inputs JSON to stdin
        proc = await asyncio.create_subprocess_exec(
            "python3", temp_filename,
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            env={
                **os.environ,
                "TENANT_ID": tenant_id,
                "REALM_ID": tenant_id,
            }
        )
        
        # Write input JSON to stdin and wait for execution (up to 30 seconds)
        input_data = json.dumps(inputs).encode()
        try:
            stdout_bytes, stderr_bytes = await asyncio.wait_for(
                proc.communicate(input=input_data),
                timeout=30.0
            )
        except asyncio.TimeoutError:
            try:
                proc.kill()
            except Exception:
                pass
            return {"error": "Script execution timed out after 30 seconds", "stdout": "", "stderr": ""}
            
        stdout = stdout_bytes.decode(errors="replace")
        stderr = stderr_bytes.decode(errors="replace")
        
        if proc.returncode != 0:
            return {
                "error": f"Script exited with code {proc.returncode}",
                "stdout": stdout,
                "stderr": stderr
            }
            
        return {
            "stdout": stdout,
            "stderr": stderr,
            "error": None
        }
    except Exception as e:
        return {
            "error": f"Failed to execute subprocess: {str(e)}",
            "stdout": "",
            "stderr": ""
        }
    finally:
        # Clean up temp file
        if os.path.exists(temp_filename):
            try:
                os.remove(temp_filename)
            except Exception:
                pass

async def main():
    # Load NATS server URL from environment, fallback to localhost:4222
    nats_url = os.environ.get("NATS_URL", "nats://localhost:4222")
    servers = nats_url.split(",")
    
    nc = NATS()
    logger.info(f"Connecting to NATS at {servers}...")
    await nc.connect(servers=servers, name="public-python-service")
    logger.info("Connected to NATS.")

    # Access JetStream context
    js = nc.jetstream()

    # Try to add stream if not exists
    try:
        await js.add_stream(name="public_python_stream", subjects=["public_python.execute.>"])
        logger.info("JetStream stream 'public_python_stream' added/updated successfully.")
    except Exception as e:
        logger.info(f"JetStream stream already exists or could not be added: {e}")

    async def execution_handler(msg):
        subject = msg.subject
        data_bytes = msg.data
        
        try:
            # Parse FIPA core.Envelope
            env = json.loads(data_bytes.decode())
            performative = env.get("perf", "")
            if performative != "request":
                logger.warning(f"Received message on {subject} with unexpected performative {performative}, ignoring.")
                await msg.ack()
                return

            body_raw = env.get("body", {})
            if isinstance(body_raw, str):
                body = json.loads(body_raw)
            else:
                body = body_raw

            script_code = body.get("script", "")
            inputs = body.get("input", {})
            tenant_id = body.get("tenant_id", "")
            return_subject = body.get("return_subject", "")

            if not return_subject:
                logger.warning(f"No return_subject provided in task request on {subject}. Terminating.")
                await msg.ack()
                return

            logger.info(f"Executing script task for tenant {tenant_id}...")
            result = await run_script(script_code, inputs, tenant_id)

            # Build core.Proof payload
            proof = {
                "task_id": env.get("id", ""),
                "type": "proof.api",
                "ts": int(time.time()),
                "data": result
            }

            # Wrap into core.Envelope
            inform_env = {
                "id": str(uuid.uuid4()),
                "ts": time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime()),
                "src": "did:toro:public-python",
                "dst": env.get("src", ""),
                "perf": "inform",
                "cid": env.get("cid", ""),
                "body": proof,
                "sig": ""
            }

            logger.info(f"Publishing result INFORM envelope to return_subject: {return_subject}")
            await nc.publish(return_subject, json.dumps(inform_env).encode())
            
            # Ack message in JetStream
            await msg.ack()
            logger.info("JetStream task successfully completed and acknowledged.")

        except Exception as e:
            logger.exception("Failed to process JetStream task")
            try:
                await msg.nak()
            except Exception:
                pass

    # Subscribe to JetStream subjects under execute stream
    sub_subject = "public_python.execute.>"
    logger.info(f"Subscribing durably to JetStream {sub_subject}...")
    await js.subscribe(
        sub_subject,
        durable="public-python-worker",
        cb=execution_handler,
        manual_ack=True
    )
    logger.info("Durable JetStream subscription active. Waiting for tasks...")

    # Keep service running
    try:
        while True:
            await asyncio.sleep(3600)
    except asyncio.CancelledError:
        logger.info("Service shutting down...")
    finally:
        await nc.drain()
        logger.info("NATS connection drained and closed.")

if __name__ == "__main__":
    asyncio.run(main())
