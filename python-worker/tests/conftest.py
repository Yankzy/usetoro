"""
Shared fixtures and environment setup for all tests.

Must set env vars BEFORE any app modules are imported, since
app.config reads them at import time.
"""
import os

# Set env vars before any app imports
os.environ["STRIPE_SECRET_KEY"] = "sk_test_mockkey123"
os.environ["STRIPE_WEBHOOK_SECRET"] = "whsec_mocksecret123"
os.environ["STRIPE_PUBLISHABLE_KEY"] = "pk_test_mockkey123"
os.environ["DATABASE_URL"] = "postgresql://test:test@localhost:5432/testdb"
os.environ["NATS_URL"] = "nats://localhost:4222"
