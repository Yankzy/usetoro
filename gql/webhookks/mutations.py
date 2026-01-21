import graphene
from webhookks.models import Event
from .types import EventNode
from webhookks.utils import dispatch_webhook

class ReplayWebhook(graphene.Mutation):
    event = graphene.Field(EventNode)
    message = graphene.String()
    status = graphene.String()

    class Arguments:
        event_id = graphene.ID(required=True)

    def mutate(self, info, event_id):
        user = info.context.user
        if not user.is_authenticated:
            raise Exception("Authentication credentials were not provided.")
        
        try:
            event = Event.objects.get(id=event_id, connection__user=user)
            # Logic to trigger replay (simulated as in REST view)
            # Todo: Trigger actual replay task
            return ReplayWebhook(event=event, message=f"Event {event.id} queued for replay", status="replayed")
        except Event.DoesNotExist:
            raise Exception("Event not found")


class DispatchWebhook(graphene.Mutation):
    """Dispatch a webhook event through Svix"""
    success = graphene.Boolean()
    message_id = graphene.String()
    message = graphene.String()
    
    class Arguments:
        event_type = graphene.String(required=True)
        payload = graphene.JSONString(required=True)
        channels = graphene.List(graphene.String)
    
    def mutate(self, info, event_type, payload, channels=None):
        user = info.context.user
        if not user.is_authenticated:
            raise Exception("Authentication credentials were not provided.")
        
        result = dispatch_webhook(
            user_id=str(user.id),
            event_type=event_type,
            payload=payload,
            channels=channels or []
        )
        
        if result["success"]:
            return DispatchWebhook(
                success=True,
                message_id=result["message_id"],
                message=f"Webhook dispatched successfully"
            )
        else:
            return DispatchWebhook(
                success=False,
                message=f"Failed to dispatch webhook: {result.get('error')}"
            )


class Mutation(graphene.ObjectType):
    replay_webhook = ReplayWebhook.Field()
    dispatch_webhook = DispatchWebhook.Field()

