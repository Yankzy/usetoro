from django.db import models
from django.conf import settings
import uuid

class WebhookSchema(models.Model):
    id = models.UUIDField(primary_key=True, default=uuid.uuid4, editable=False)
    source = models.CharField(max_length=255, unique=True, help_text="e.g. 'stripe_charge_succeeded'")
    schema_definition = models.JSONField(help_text="JSON Schema definition used to generate Pydantic validation model")
    version = models.IntegerField(default=1)
    is_active = models.BooleanField(default=True)
    created_at = models.DateTimeField(auto_now_add=True)
    updated_at = models.DateTimeField(auto_now=True)

    def __str__(self):
        return f"{self.source} (v{self.version})"

class Connection(models.Model):
    id = models.UUIDField(primary_key=True, default=uuid.uuid4, editable=False)
    user = models.ForeignKey(settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name='connections')
    source_type = models.CharField(max_length=50) # e.g., stripe, custom
    secret = models.CharField(max_length=255)
    is_active = models.BooleanField(default=True)
    created_at = models.DateTimeField(auto_now_add=True)

    def __str__(self):
        return f"{self.source_type} ({self.user.username})"

class Event(models.Model):
    id = models.UUIDField(primary_key=True, default=uuid.uuid4, editable=False)
    connection = models.ForeignKey(Connection, on_delete=models.CASCADE, related_name='events')
    source = models.CharField(max_length=255)
    payload = models.JSONField()
    headers = models.JSONField(default=dict)
    status = models.CharField(max_length=50, default='received')
    created_at = models.DateTimeField(auto_now_add=True)

    def __str__(self):
        return f"{self.source} - {self.id}"

class DeliveryAttempt(models.Model):
    id = models.UUIDField(primary_key=True, default=uuid.uuid4, editable=False)
    event = models.ForeignKey(Event, on_delete=models.CASCADE, related_name='delivery_attempts')
    destination_url = models.URLField()
    status = models.CharField(max_length=50) # success, failed
    response_code = models.IntegerField(null=True, blank=True)
    response_body = models.TextField(null=True, blank=True)
    attempted_at = models.DateTimeField(auto_now_add=True)
