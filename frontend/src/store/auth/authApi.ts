import { baseApi } from '../baseApi';
import { setCredentials } from './authSlice';

export const authApi = baseApi.injectEndpoints({
    endpoints: (builder) => ({
        login: builder.mutation({
            query: (credentials) => ({
                url: '/login/',
                method: 'POST',
                body: credentials,
            }),
            async onQueryStarted(_, { dispatch, queryFulfilled }) {
                try {
                    const { data } = await queryFulfilled;
                    dispatch(setCredentials(data));
                } catch (err) {
                    console.error("Login failed", err);
                }
            },
        }),
    }),
});

export const { useLoginMutation } = authApi;
