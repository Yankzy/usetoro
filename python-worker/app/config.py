import os

def load_dotenv():
    base_dir = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
    candidates = [
        os.path.join(base_dir, "container", ".env"),
        os.path.join(base_dir, ".env"),
        ".env",
    ]
    for candidate in candidates:
        if os.path.exists(candidate):
            with open(candidate, "r") as f:
                for line in f:
                    line = line.strip()
                    if line and not line.startswith("#") and "=" in line:
                        k, v = line.split("=", 1)
                        k = k.strip()
                        v = v.strip().strip("'\"")
                        if k not in os.environ:
                            os.environ[k] = v
            break

load_dotenv()

STRIPE_SECRET_KEY: str = os.environ.get("STRIPE_SECRET_KEY", "")
STRIPE_WEBHOOK_SECRET: str = os.environ.get("STRIPE_WEBHOOK_SECRET", "")
STRIPE_PUBLISHABLE_KEY: str = os.environ.get("STRIPE_PUBLISHABLE_KEY", "")
DATABASE_URL: str = os.environ.get("DATABASE_URL", "")
NATS_URL: str = os.environ.get("NATS_URL", "nats://localhost:4222")
OPENAI_API_KEY: str = os.environ.get("OPENAI_API_KEY", "")

