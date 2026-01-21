import { configureStore } from '@reduxjs/toolkit';
import { setupListeners } from '@reduxjs/toolkit/query';
import { baseApi, graphqlApi } from './baseApi';
import authReducer from './auth/authSlice';
import dashboardReducer from './dashboard/dashboardSlice';

export const store = configureStore({
    reducer: {
        [baseApi.reducerPath]: baseApi.reducer,
        [graphqlApi.reducerPath]: graphqlApi.reducer,
        auth: authReducer,
        dashboard: dashboardReducer,
    },
    middleware: (getDefaultMiddleware) =>
        getDefaultMiddleware().concat(baseApi.middleware, graphqlApi.middleware),
});

setupListeners(store.dispatch);

export type RootState = ReturnType<typeof store.getState>;
export type AppDispatch = typeof store.dispatch;
export default store;
