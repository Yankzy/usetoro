import { createApi, fetchBaseQuery } from '@reduxjs/toolkit/query/react';
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

export const baseApi = createApi({
    reducerPath: 'api',
    baseQuery: baseQuery,
    tagTypes: ['User', 'Webhook'],
    endpoints: () => ({}),
});
