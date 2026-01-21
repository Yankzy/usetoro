import { graphqlApi } from '../baseApi';
import { GET_USERS, REGISTER_USER, LOGIN_USER } from './userQueries';
import { setCredentials } from '../auth/authSlice';
import type { GetUsersResponse } from './userTypes';

export const userApi = graphqlApi.injectEndpoints({
    endpoints: (builder) => ({
        getUsers: builder.query<GetUsersResponse, void>({
            query: () => ({
                document: GET_USERS,
            }),
        }),
        registerUser: builder.mutation<any, any>({
            query: (userData) => ({
                document: REGISTER_USER,
                variables: userData,
            }),
        }),
        loginUser: builder.mutation<any, any>({
            query: (credentials) => ({
                document: LOGIN_USER,
                variables: credentials,
            }),
            async onQueryStarted(_, { dispatch, queryFulfilled }) {
                try {
                    const { data } = await queryFulfilled;
                    dispatch(setCredentials({
                        user: data.loginUser.user,
                        token: data.loginUser.token
                    }));
                } catch (err) {
                    console.error("Login failed", err);
                }
            },
        }),
    }),
});

export const { useGetUsersQuery, useRegisterUserMutation, useLoginUserMutation } = userApi;
