"""
Shared fixtures and environment setup for all tests.

Must set env vars BEFORE any app modules are imported, since
app.config reads them at import time.
"""
import os
import sys
from pathlib import Path

tests_dir = Path(__file__).resolve().parent
worker_root = tests_dir.parent
app_dir = worker_root / "app"

for p in [str(worker_root), str(app_dir)]:
    if p not in sys.path:
        sys.path.insert(0, p)

# Set env vars before any app imports
os.environ["STRIPE_SECRET_KEY"] = "sk_test_mockkey123"
os.environ["STRIPE_WEBHOOK_SECRET"] = "whsec_mocksecret123"
os.environ["STRIPE_PUBLISHABLE_KEY"] = "pk_test_mockkey123"
os.environ["DATABASE_URL"] = "postgresql://test:test@localhost:5432/testdb"
os.environ["NATS_URL"] = "nats://localhost:4222"

