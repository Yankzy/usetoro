from rest_framework import generics, permissions, status
from rest_framework.response import Response
from rest_framework.views import APIView
from .models import Event, Connection
from .serializers import EventSerializer

class WebhookListView(generics.ListAPIView):
    serializer_class = EventSerializer
    permission_classes = [permissions.IsAuthenticated]

    def get_queryset(self):
        # Filter events by the user's connections
        return Event.objects.filter(connection__user=self.request.user).order_by('-created_at')

class ReplayWebhookView(APIView):
    permission_classes = [permissions.IsAuthenticated]

    def post(self, request, event_id):
        try:
            event = Event.objects.get(id=event_id, connection__user=request.user)
            # Todo: Trigger actual replay logic here (e.g. celery task)
            # For now just update status or log
            return Response({"status": "replayed", "message": f"Event {event.id} queued for replay"}, status=status.HTTP_200_OK)
        except Event.DoesNotExist:
            return Response({"error": "Event not found"}, status=status.HTTP_404_NOT_FOUND)
