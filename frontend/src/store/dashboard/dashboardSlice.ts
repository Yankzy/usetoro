import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

interface DashboardState {
    selectedWebhookId: string | null;
    isReplayModalOpen: boolean;
}

const initialState: DashboardState = {
    selectedWebhookId: null,
    isReplayModalOpen: false,
};

const dashboardSlice = createSlice({
    name: 'dashboard',
    initialState,
    reducers: {
        setSelectedWebhookId: (state, action: PayloadAction<string | null>) => {
            state.selectedWebhookId = action.payload;
        },
        toggleReplayModal: (state) => {
            state.isReplayModalOpen = !state.isReplayModalOpen;
        },
    },
});

export const { setSelectedWebhookId, toggleReplayModal } = dashboardSlice.actions;
export default dashboardSlice.reducer;
