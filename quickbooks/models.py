from django.db import models
from cryptography.fernet import Fernet
from django.conf import settings
import os
import uuid

# Helper for simple encryption (in a real app, use a proper library/helper or existing project util)
# For this scaffolding, assuming settings.FERNET_KEY exists or we use a hardcoded key for demo scafolding if not provided.
# NOTE: In production, never hardcode keys. We'll assume settings has a key.


class QuickBooksConnection(models.Model):
    id = models.UUIDField(primary_key=True, default=uuid.uuid4, editable=False)
    realm_id = models.CharField(max_length=255, unique=True, help_text="QuickBooks Realm ID (Company ID)")
    # Storing encrypted tokens meant implementing a custom field or manual handling. 
    # For scaffolding, we'll store them as TextFields and assume helper methods handle encryption/decryption,
    # or use a property. Here is a simple implementation using properties.
    
    _access_token = models.TextField(db_column='access_token', help_text="Encrypted Access Token")
    _refresh_token = models.TextField(db_column='refresh_token', help_text="Encrypted Refresh Token")
    
    last_refreshed = models.DateTimeField(auto_now=True)
    status = models.CharField(
        max_length=50, 
        choices=[('active', 'Active'), ('disconnected', 'Disconnected')],
        default='active'
    )
    created_at = models.DateTimeField(auto_now_add=True)
    updated_at = models.DateTimeField(auto_now=True)

    def set_tokens(self, access_token, refresh_token):
        f = Fernet(settings.FERNET_KEY)
        self._access_token = f.encrypt(access_token.encode()).decode()
        self._refresh_token = f.encrypt(refresh_token.encode()).decode()

    def get_access_token(self):
        f = Fernet(settings.FERNET_KEY)
        return f.decrypt(self._access_token.encode()).decode()

    def get_refresh_token(self):
        f = Fernet(settings.FERNET_KEY)
        return f.decrypt(self._refresh_token.encode()).decode()

    def __str__(self):
        return f"QuickBooks Connection {self.realm_id} ({self.status})"
