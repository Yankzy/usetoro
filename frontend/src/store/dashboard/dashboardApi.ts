import { graphqlApi } from '../baseApi';
import { gql } from 'graphql-request';

export interface WebhookEvent {
    id: string;
    source: string;
    payload: any;
    status: string;
    created_at: string;
    delivery_attempts: any[];
}

export interface SvixMessage {
    id: string;
    event_type: string;
    payload: any;
    channels: string[];
    timestamp: string;
}

export interface SvixMessageAttempt {
    id: string;
    msg_id: string;
    status: string;
    response_status_code: number | null;
    timestamp: string;
    endpoint_id: string;
    url: string;
}

export const dashboardApi = graphqlApi.injectEndpoints({
    endpoints: (builder) => ({
        getWebhooks: builder.query<WebhookEvent[], void>({
            query: () => ({
                document: gql`
                    query GetEvents {
                        events {
                            id
                            source
                            payload
                            status
                            created_at: createdAt
                            delivery_attempts: deliveryAttempts {
                                status
                                responseCode
                                attemptedAt
                            }
                        }
                    }
                `,
            }),
            transformResponse: (response: { events: WebhookEvent[] }) => response.events,
            providesTags: ['Webhook'],
        }),

        // New Svix webhook queries
        getSvixMessages: builder.query<SvixMessage[], { limit?: number }>({
            query: ({ limit = 50 }) => ({
                document: gql`
                    query GetWebhookMessages($limit: Int) {
                        webhookMessages(limit: $limit) {
                            id
                            eventType
                            payload
                            channels
                            timestamp
                        }
                    }
                `,
                variables: { limit },
            }),
            transformResponse: (response: { webhookMessages: SvixMessage[] }) => response.webhookMessages,
            providesTags: ['Webhook'],
        }),

        getSvixAttempts: builder.query<SvixMessageAttempt[], { limit?: number }>({
            query: ({ limit = 50 }) => ({
                document: gql`
                    query GetWebhookAttempts($limit: Int) {
                        webhookAttempts(limit: $limit) {
                            id
                            msgId
                            status
                            responseStatusCode
                            timestamp
                            endpointId
                            url
                        }
                    }
                `,
                variables: { limit },
            }),
            transformResponse: (response: { webhookAttempts: SvixMessageAttempt[] }) => response.webhookAttempts,
            providesTags: ['Webhook'],
        }),

        replayWebhook: builder.mutation<{ replayWebhook: { status: string; message: string } }, string>({
            query: (id) => ({
                document: gql`
                    mutation ReplayEvent($eventId: ID!) {
                        replayWebhook(eventId: $eventId) {
                            status
                            message
                            event {
                                id
                                status
                            }
                        }
                    }
                `,
                variables: { eventId: id },
            }),
            invalidatesTags: ['Webhook'],
        }),

        dispatchWebhook: builder.mutation<
            { dispatchWebhook: { success: boolean; messageId?: string; message: string } },
            { eventType: string; payload: any; channels?: string[] }
        >({
            query: ({ eventType, payload, channels }) => ({
                document: gql`
                    mutation DispatchWebhook($eventType: String!, $payload: JSONString!, $channels: [String]) {
                        dispatchWebhook(eventType: $eventType, payload: $payload, channels: $channels) {
                            success
                            messageId
                            message
                        }
                    }
                `,
                variables: { eventType, payload, channels },
            }),
            invalidatesTags: ['Webhook'],
        }),
    }),
});

export const {
    useGetWebhooksQuery,
    useGetSvixMessagesQuery,
    useGetSvixAttemptsQuery,
    useReplayWebhookMutation,
    useDispatchWebhookMutation
} = dashboardApi;

