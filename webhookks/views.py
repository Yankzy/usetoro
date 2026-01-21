"""
Webhook Views

All webhook API access is handled via GraphQL.
See gql/webhookks/queries.py for available queries:
- webhook_messages(limit: Int)
- webhook_attempts(limit: Int)

Frontend should use GraphQL hooks from dashboardApi.ts:
- useGetSvixMessagesQuery()
- useGetSvixAttemptsQuery()
"""
