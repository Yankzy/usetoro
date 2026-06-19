import os

STRIPE_SECRET_KEY: str = os.environ["STRIPE_SECRET_KEY"]
STRIPE_WEBHOOK_SECRET: str = os.environ["STRIPE_WEBHOOK_SECRET"]
STRIPE_PUBLISHABLE_KEY: str = os.environ["STRIPE_PUBLISHABLE_KEY"]
DATABASE_URL: str = os.environ["DATABASE_URL"]
NATS_URL: str = os.environ.get("NATS_URL", "nats://localhost:4222")
