import { baseApi } from '../baseApi';

export interface WebhookEvent {
    id: string;
    source: string;
    payload: any;
    status: string;
    created_at: string;
    delivery_attempts: any[];
}

export const dashboardApi = baseApi.injectEndpoints({
    endpoints: (builder) => ({
        getWebhooks: builder.query<WebhookEvent[], void>({
            query: () => '/webhooks/',
            providesTags: ['Webhook'],
        }),
        replayWebhook: builder.mutation<{ status: string; message: string }, string>({
            query: (id) => ({
                url: `/webhooks/${id}/replay/`,
                method: 'POST',
            }),
            invalidatesTags: ['Webhook'],
        }),
    }),
});

export const { useGetWebhooksQuery, useReplayWebhookMutation } = dashboardApi;
