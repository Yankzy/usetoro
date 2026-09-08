from uuid import uuid4

from django.db import models

from ledger.models.mixins import CreateUpdateMixIn


class TokenCountModel(CreateUpdateMixIn):
    """
    Minimal token usage tracker.
    """
    uuid = models.UUIDField(primary_key=True, default=uuid4, editable=False)
    entity_name = models.CharField(max_length=150, null=True, blank=True) # The name of the entity that the usage belongs to
    agent_name = models.CharField(max_length=150, null=True, blank=True) # The name of the agent that used the tokens
    num_requests = models.PositiveIntegerField(default=0) # The number of requests made by the agent
    total_tokens = models.PositiveIntegerField(default=0) # The total number of tokens used by the agent
    metadata = models.JSONField(default=dict, null=True, blank=True) # The metadata of the usage
    created_at = models.DateTimeField(auto_now_add=True, null=True, blank=True) # The date and time the usage was created

    class Meta:
        ordering = ['-created_at']
        indexes = [
            models.Index(fields=['created_at']),
        ]

    def __str__(self):
        return f'{self.agent_name} → {self.num_requests} requests, {self.total_tokens} total tokens'


