import graphene
from graphene_django import DjangoObjectType
from webhookks.models import Event, Connection, DeliveryAttempt

class ConnectionNode(DjangoObjectType):
    class Meta:
        model = Connection
        fields = ("id", "source_type", "is_active", "created_at")

class DeliveryAttemptNode(DjangoObjectType):
    class Meta:
        model = DeliveryAttempt
        fields = ("destination_url", "status", "response_code", "attempted_at")

class EventNode(DjangoObjectType):
    delivery_attempts = graphene.List(DeliveryAttemptNode)

    class Meta:
        model = Event
        fields = ("id", "source", "payload", "status", "created_at")

    def resolve_delivery_attempts(self, info):
        return self.delivery_attempts.all()


# Svix-specific types for webhook messages and attempts
class SvixMessageType(graphene.ObjectType):
    """Represents a webhook message in Svix"""
    id = graphene.String(required=True)
    event_type = graphene.String(required=True)
    payload = graphene.JSONString(required=True)
    channels = graphene.List(graphene.String)
    timestamp = graphene.String()


class SvixMessageAttemptType(graphene.ObjectType):
    """Represents a webhook delivery attempt in Svix"""
    id = graphene.String(required=True)
    msg_id = graphene.String(required=True)
    status = graphene.String(required=True)
    response_status_code = graphene.Int()
    timestamp = graphene.String()
    endpoint_id = graphene.String()
    url = graphene.String()
