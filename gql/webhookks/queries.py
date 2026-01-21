import graphene
from .types import EventNode, SvixMessageType, SvixMessageAttemptType
from webhookks.models import Event
from webhookks.utils import get_webhook_messages, get_webhook_attempts

class Query(graphene.ObjectType):
    events = graphene.List(EventNode)
    
    # Svix webhook queries
    webhook_messages = graphene.List(
        SvixMessageType,
        limit=graphene.Int(default_value=50)
    )
    webhook_attempts = graphene.List(
        SvixMessageAttemptType,
        limit=graphene.Int(default_value=50)
    )

    def resolve_events(self, info):
        user = info.context.user
        if not user.is_authenticated:
            return Event.objects.none()
        return Event.objects.filter(connection__user=user).order_by('-created_at')
    
    def resolve_webhook_messages(self, info, limit=50):
        """
        Retrieve webhook messages from Svix for the authenticated user.
        """
        user = info.context.user
        if not user.is_authenticated:
            return []
        
        messages = get_webhook_messages(user_id=str(user.id), limit=limit)
        return messages
    
    def resolve_webhook_attempts(self, info, limit=50):
        """
        Retrieve webhook delivery attempts from Svix for the authenticated user.
        """
        user = info.context.user
        if not user.is_authenticated:
            return []
        
        attempts = get_webhook_attempts(user_id=str(user.id), limit=limit)
        return attempts

