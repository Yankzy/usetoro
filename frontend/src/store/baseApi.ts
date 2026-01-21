import { createApi, fetchBaseQuery } from '@reduxjs/toolkit/query/react';
import { graphqlRequestBaseQuery } from '@rtk-query/graphql-request-base-query';
import ENV from '../env';

// Create a base query instance to be reused
const baseQuery = fetchBaseQuery({
    baseUrl: ENV.API_URL,
    prepareHeaders: (headers, { getState }) => {
        // Type assertion to avoid circular import of RootState
        const token = (getState() as any).auth?.token;
        if (token) {
            headers.set('authorization', `Bearer ${token}`);
        }
        return headers;
    },
});

export const baseGraphQLQuery = graphqlRequestBaseQuery({
    url: ENV.API_URL.replace(/\/api\/?$/, '/toro_graphql/'),
    prepareHeaders: async (headers, { getState }) => {
        const token = (getState() as any).auth?.token;
        if (token) {
            headers.set('authorization', `Bearer ${token}`);
        }
        return headers;
    },
});

export const baseApi = createApi({
    reducerPath: 'api',
    baseQuery: baseQuery,
    tagTypes: ['User', 'Webhook'],
    endpoints: () => ({}),
});

export const graphqlApi = createApi({
    reducerPath: 'graphqlApi',
    baseQuery: baseGraphQLQuery,
    tagTypes: ['User', 'Webhook'],
    endpoints: () => ({}),
});
