import asyncio
import os
import signal
import json
from dotenv import load_dotenv
import nats
from nats.errors import ConnectionClosedError, TimeoutError, NoRespondersError

# Load environment variables
load_dotenv()

async def main():
    print("🐍 Python NATS Worker Starting...", flush=True)

    nats_url = os.getenv("NATS_URL", "nats://localhost:4222")
    servers = nats_url.split(",")
    
    # 1. Connect to NATS
    try:
        # We pass the list of servers. The client will connect to one and discover the rest.
        nc = await nats.connect(servers=servers, name="python-worker")
        print(f"✅ Connected to NATS cluster at {servers}", flush=True)
    except Exception as e:
        print(f"❌ Failed to connect to NATS: {e}", flush=True)
        return

    # 2. Define Message Handler
    async def message_handler(msg):
        subject = msg.subject
        reply = msg.reply
        data = msg.data.decode()
        
        print(f"Processing message on [{subject}]: {data}", flush=True)
        
        # Determine skill based on subject
        # e.g. skill.ocr -> perform OCR
        response = {}
        
        if subject == "skill.ocr":
            # Mock OCR processing
            response = {"processed": True, "text": "Extracted text from python worker", "original_len": len(data)}
        else:
            response = {"status": "unknown_skill", "subject": subject}

        # Reply if reply inbox is present (Request-Reply pattern)
        if reply:
            await msg.respond(json.dumps(response).encode())
            print(f"Replied to {reply}", flush=True)
        else:
            print("No reply inbox found, ignoring response", flush=True)

    # 3. Subscribe with Queue Group
    # "workers" queue group ensures load balancing if we run multiple instances
    sub = await nc.subscribe("skill.>", queue="workers", cb=message_handler)
    print("🎧 Subscribed to 'skill.>'", flush=True)

    # 4. Graceful Shutdown
    stop_event = asyncio.Event()

    def signal_handler():
        print("🛑 Shutdown signal received", flush=True)
        stop_event.set()

    loop = asyncio.get_running_loop()
    for sig in (signal.SIGINT, signal.SIGTERM):
        loop.add_signal_handler(sig, signal_handler)

    # Keep running until signal
    await stop_event.wait()

    # Draining connection
    print("Draining NATS connection...", flush=True)
    await nc.drain()
    print("👋 Python Worker Exited.", flush=True)

if __name__ == "__main__":
    try:
        asyncio.run(main())
    except KeyboardInterrupt:
        pass
