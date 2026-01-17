from django.urls import path
from .api_views import WebhookListView, ReplayWebhookView

urlpatterns = [
    path('webhooks/', WebhookListView.as_view(), name='webhook-list'),
    path('webhooks/<uuid:event_id>/replay/', ReplayWebhookView.as_view(), name='webhook-replay'),
]
