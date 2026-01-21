from django.urls import path

urlpatterns = [
    # All webhook APIs use GraphQL (see gql/webhookks/queries.py)
    # Available queries: webhook_messages, webhook_attempts
    # Frontend: useGetSvixMessagesQuery(), useGetSvixAttemptsQuery()
]
