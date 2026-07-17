package mailpool_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Yankzy/usetoro/internal/services/mailpool"
	"github.com/stretchr/testify/require"
)

func TestMailpoolGeneratedMethods(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	client, err := mailpool.NewMailpool(ts.URL, "test-api-key")
	require.NoError(t, err)

	ctx := context.Background()


	t.Run("GetDomainsWithResponse", func(t *testing.T) {
		_, err := client.GetDomainsWithResponse(ctx, &mailpool.GetDomainsParams{})
		require.NoError(t, err, "Expected no error for GetDomainsWithResponse")
	})

	t.Run("PostDomainsWithBodyWithResponse", func(t *testing.T) {
		_, err := client.PostDomainsWithBodyWithResponse(ctx, "test", strings.NewReader(`{}`))
		require.NoError(t, err, "Expected no error for PostDomainsWithBodyWithResponse")
	})

	t.Run("PostDomainsWithResponse", func(t *testing.T) {
		_, err := client.PostDomainsWithResponse(ctx, mailpool.PostDomainsJSONRequestBody{})
		require.NoError(t, err, "Expected no error for PostDomainsWithResponse")
	})

	t.Run("PostDomainsGoogleWorkspaceAvailabilityWithBodyWithResponse", func(t *testing.T) {
		_, err := client.PostDomainsGoogleWorkspaceAvailabilityWithBodyWithResponse(ctx, "test", strings.NewReader(`{}`))
		require.NoError(t, err, "Expected no error for PostDomainsGoogleWorkspaceAvailabilityWithBodyWithResponse")
	})

	t.Run("PostDomainsGoogleWorkspaceAvailabilityWithResponse", func(t *testing.T) {
		_, err := client.PostDomainsGoogleWorkspaceAvailabilityWithResponse(ctx, mailpool.PostDomainsGoogleWorkspaceAvailabilityJSONRequestBody{})
		require.NoError(t, err, "Expected no error for PostDomainsGoogleWorkspaceAvailabilityWithResponse")
	})

	t.Run("GetDomainInfoWithResponse", func(t *testing.T) {
		_, err := client.GetDomainInfoWithResponse(ctx, &mailpool.GetDomainInfoParams{})
		require.NoError(t, err, "Expected no error for GetDomainInfoWithResponse")
	})

	t.Run("GetDomainsOwnersWithResponse", func(t *testing.T) {
		_, err := client.GetDomainsOwnersWithResponse(ctx, &mailpool.GetDomainsOwnersParams{})
		require.NoError(t, err, "Expected no error for GetDomainsOwnersWithResponse")
	})

	t.Run("PostDomainsOwnersWithBodyWithResponse", func(t *testing.T) {
		_, err := client.PostDomainsOwnersWithBodyWithResponse(ctx, "test", strings.NewReader(`{}`))
		require.NoError(t, err, "Expected no error for PostDomainsOwnersWithBodyWithResponse")
	})

	t.Run("PostDomainsOwnersWithResponse", func(t *testing.T) {
		_, err := client.PostDomainsOwnersWithResponse(ctx, mailpool.PostDomainsOwnersJSONRequestBody{})
		require.NoError(t, err, "Expected no error for PostDomainsOwnersWithResponse")
	})

	t.Run("DeleteDomainsOwnersOwnerIdWithResponse", func(t *testing.T) {
		_, err := client.DeleteDomainsOwnersOwnerIdWithResponse(ctx, "test")
		require.NoError(t, err, "Expected no error for DeleteDomainsOwnersOwnerIdWithResponse")
	})

	t.Run("PutDomainsOwnersOwnerIdWithBodyWithResponse", func(t *testing.T) {
		_, err := client.PutDomainsOwnersOwnerIdWithBodyWithResponse(ctx, "test", "test", strings.NewReader(`{}`))
		require.NoError(t, err, "Expected no error for PutDomainsOwnersOwnerIdWithBodyWithResponse")
	})

	t.Run("PutDomainsOwnersOwnerIdWithResponse", func(t *testing.T) {
		_, err := client.PutDomainsOwnersOwnerIdWithResponse(ctx, "test", mailpool.PutDomainsOwnersOwnerIdJSONRequestBody{})
		require.NoError(t, err, "Expected no error for PutDomainsOwnersOwnerIdWithResponse")
	})

	t.Run("PostDomainsRenewWithBodyWithResponse", func(t *testing.T) {
		_, err := client.PostDomainsRenewWithBodyWithResponse(ctx, "test", strings.NewReader(`{}`))
		require.NoError(t, err, "Expected no error for PostDomainsRenewWithBodyWithResponse")
	})

	t.Run("PostDomainsRenewWithResponse", func(t *testing.T) {
		_, err := client.PostDomainsRenewWithResponse(ctx, mailpool.PostDomainsRenewJSONRequestBody{})
		require.NoError(t, err, "Expected no error for PostDomainsRenewWithResponse")
	})

	t.Run("GetDomainsRenewDomainsWithResponse", func(t *testing.T) {
		_, err := client.GetDomainsRenewDomainsWithResponse(ctx, &mailpool.GetDomainsRenewDomainsParams{})
		require.NoError(t, err, "Expected no error for GetDomainsRenewDomainsWithResponse")
	})

	t.Run("GetDomainsRenewOrdersOrderIdWithResponse", func(t *testing.T) {
		_, err := client.GetDomainsRenewOrdersOrderIdWithResponse(ctx, 1)
		require.NoError(t, err, "Expected no error for GetDomainsRenewOrdersOrderIdWithResponse")
	})

	t.Run("GetDomainsRenewPricesWithResponse", func(t *testing.T) {
		_, err := client.GetDomainsRenewPricesWithResponse(ctx, &mailpool.GetDomainsRenewPricesParams{})
		require.NoError(t, err, "Expected no error for GetDomainsRenewPricesWithResponse")
	})

	t.Run("GetDomainsSuggestionsWithResponse", func(t *testing.T) {
		_, err := client.GetDomainsSuggestionsWithResponse(ctx, &mailpool.GetDomainsSuggestionsParams{})
		require.NoError(t, err, "Expected no error for GetDomainsSuggestionsWithResponse")
	})

	t.Run("PostDomainsTransferWithBodyWithResponse", func(t *testing.T) {
		_, err := client.PostDomainsTransferWithBodyWithResponse(ctx, "test", strings.NewReader(`{}`))
		require.NoError(t, err, "Expected no error for PostDomainsTransferWithBodyWithResponse")
	})

	t.Run("PostDomainsTransferWithResponse", func(t *testing.T) {
		_, err := client.PostDomainsTransferWithResponse(ctx, mailpool.PostDomainsTransferJSONRequestBody{})
		require.NoError(t, err, "Expected no error for PostDomainsTransferWithResponse")
	})

	t.Run("DeleteDomainsDomainIdWithResponse", func(t *testing.T) {
		_, err := client.DeleteDomainsDomainIdWithResponse(ctx, "test")
		require.NoError(t, err, "Expected no error for DeleteDomainsDomainIdWithResponse")
	})

	t.Run("GetDomainsDomainIdWithResponse", func(t *testing.T) {
		_, err := client.GetDomainsDomainIdWithResponse(ctx, "test")
		require.NoError(t, err, "Expected no error for GetDomainsDomainIdWithResponse")
	})

	t.Run("PostDomainsDomainIdDmarcEmailWithBodyWithResponse", func(t *testing.T) {
		_, err := client.PostDomainsDomainIdDmarcEmailWithBodyWithResponse(ctx, "test", "test", strings.NewReader(`{}`))
		require.NoError(t, err, "Expected no error for PostDomainsDomainIdDmarcEmailWithBodyWithResponse")
	})

	t.Run("PostDomainsDomainIdDmarcEmailWithResponse", func(t *testing.T) {
		_, err := client.PostDomainsDomainIdDmarcEmailWithResponse(ctx, "test", mailpool.PostDomainsDomainIdDmarcEmailJSONRequestBody{})
		require.NoError(t, err, "Expected no error for PostDomainsDomainIdDmarcEmailWithResponse")
	})

	t.Run("PostDomainsDomainIdDmarcPolicyWithBodyWithResponse", func(t *testing.T) {
		_, err := client.PostDomainsDomainIdDmarcPolicyWithBodyWithResponse(ctx, "test", "test", strings.NewReader(`{}`))
		require.NoError(t, err, "Expected no error for PostDomainsDomainIdDmarcPolicyWithBodyWithResponse")
	})

	t.Run("PostDomainsDomainIdDmarcPolicyWithResponse", func(t *testing.T) {
		_, err := client.PostDomainsDomainIdDmarcPolicyWithResponse(ctx, "test", mailpool.PostDomainsDomainIdDmarcPolicyJSONRequestBody{})
		require.NoError(t, err, "Expected no error for PostDomainsDomainIdDmarcPolicyWithResponse")
	})

	t.Run("GetDomainsDnsWithResponse", func(t *testing.T) {
		_, err := client.GetDomainsDnsWithResponse(ctx, "test")
		require.NoError(t, err, "Expected no error for GetDomainsDnsWithResponse")
	})

	t.Run("PutDomainsDnsWithBodyWithResponse", func(t *testing.T) {
		_, err := client.PutDomainsDnsWithBodyWithResponse(ctx, "test", "test", strings.NewReader(`{}`))
		require.NoError(t, err, "Expected no error for PutDomainsDnsWithBodyWithResponse")
	})

	t.Run("PutDomainsDnsWithResponse", func(t *testing.T) {
		_, err := client.PutDomainsDnsWithResponse(ctx, "test", mailpool.PutDomainsDnsJSONRequestBody{})
		require.NoError(t, err, "Expected no error for PutDomainsDnsWithResponse")
	})

	t.Run("PutDomainsDomainIdOwnerWithBodyWithResponse", func(t *testing.T) {
		_, err := client.PutDomainsDomainIdOwnerWithBodyWithResponse(ctx, "test", "test", strings.NewReader(`{}`))
		require.NoError(t, err, "Expected no error for PutDomainsDomainIdOwnerWithBodyWithResponse")
	})

	t.Run("PutDomainsDomainIdOwnerWithResponse", func(t *testing.T) {
		_, err := client.PutDomainsDomainIdOwnerWithResponse(ctx, "test", mailpool.PutDomainsDomainIdOwnerJSONRequestBody{})
		require.NoError(t, err, "Expected no error for PutDomainsDomainIdOwnerWithResponse")
	})

	t.Run("PostDomainsRedirectUrlWithBodyWithResponse", func(t *testing.T) {
		_, err := client.PostDomainsRedirectUrlWithBodyWithResponse(ctx, "test", "test", strings.NewReader(`{}`))
		require.NoError(t, err, "Expected no error for PostDomainsRedirectUrlWithBodyWithResponse")
	})

	t.Run("PostDomainsRedirectUrlWithResponse", func(t *testing.T) {
		_, err := client.PostDomainsRedirectUrlWithResponse(ctx, "test", mailpool.PostDomainsRedirectUrlJSONRequestBody{})
		require.NoError(t, err, "Expected no error for PostDomainsRedirectUrlWithResponse")
	})

	t.Run("PostDomainsDomainIdTransferOutAuthCodeWithResponse", func(t *testing.T) {
		_, err := client.PostDomainsDomainIdTransferOutAuthCodeWithResponse(ctx, 1)
		require.NoError(t, err, "Expected no error for PostDomainsDomainIdTransferOutAuthCodeWithResponse")
	})

	t.Run("PostDomainsDomainIdTransferOutAuthorizeWithBodyWithResponse", func(t *testing.T) {
		_, err := client.PostDomainsDomainIdTransferOutAuthorizeWithBodyWithResponse(ctx, 1, "test", strings.NewReader(`{}`))
		require.NoError(t, err, "Expected no error for PostDomainsDomainIdTransferOutAuthorizeWithBodyWithResponse")
	})

	t.Run("PostDomainsDomainIdTransferOutAuthorizeWithResponse", func(t *testing.T) {
		_, err := client.PostDomainsDomainIdTransferOutAuthorizeWithResponse(ctx, 1, mailpool.PostDomainsDomainIdTransferOutAuthorizeJSONRequestBody{})
		require.NoError(t, err, "Expected no error for PostDomainsDomainIdTransferOutAuthorizeWithResponse")
	})

	t.Run("ReceiveWebhookWithBodyWithResponse", func(t *testing.T) {
		_, err := client.ReceiveWebhookWithBodyWithResponse(ctx, "test", strings.NewReader(`{}`))
		require.NoError(t, err, "Expected no error for ReceiveWebhookWithBodyWithResponse")
	})

	t.Run("ReceiveWebhookWithResponse", func(t *testing.T) {
		_, err := client.ReceiveWebhookWithResponse(ctx, mailpool.ReceiveWebhookJSONRequestBody{})
		require.NoError(t, err, "Expected no error for ReceiveWebhookWithResponse")
	})

	t.Run("GetExportsBatchesWithResponse", func(t *testing.T) {
		_, err := client.GetExportsBatchesWithResponse(ctx, &mailpool.GetExportsBatchesParams{})
		require.NoError(t, err, "Expected no error for GetExportsBatchesWithResponse")
	})

	t.Run("GetExportsBatchesBatchIdWithResponse", func(t *testing.T) {
		_, err := client.GetExportsBatchesBatchIdWithResponse(ctx, 1)
		require.NoError(t, err, "Expected no error for GetExportsBatchesBatchIdWithResponse")
	})

	t.Run("PostExportsCredentialsWithBodyWithResponse", func(t *testing.T) {
		_, err := client.PostExportsCredentialsWithBodyWithResponse(ctx, "test", strings.NewReader(`{}`))
		require.NoError(t, err, "Expected no error for PostExportsCredentialsWithBodyWithResponse")
	})

	t.Run("PostExportsCredentialsWithResponse", func(t *testing.T) {
		_, err := client.PostExportsCredentialsWithResponse(ctx, mailpool.PostExportsCredentialsJSONRequestBody{})
		require.NoError(t, err, "Expected no error for PostExportsCredentialsWithResponse")
	})

	t.Run("GetExportsCredentialsPlatformWithResponse", func(t *testing.T) {
		_, err := client.GetExportsCredentialsPlatformWithResponse(ctx, mailpool.ExportPlatform("test"))
		require.NoError(t, err, "Expected no error for GetExportsCredentialsPlatformWithResponse")
	})

	t.Run("PostExportsMailboxesWithBodyWithResponse", func(t *testing.T) {
		_, err := client.PostExportsMailboxesWithBodyWithResponse(ctx, "test", strings.NewReader(`{}`))
		require.NoError(t, err, "Expected no error for PostExportsMailboxesWithBodyWithResponse")
	})

	t.Run("PostExportsMailboxesWithResponse", func(t *testing.T) {
		_, err := client.PostExportsMailboxesWithResponse(ctx, mailpool.PostExportsMailboxesJSONRequestBody{})
		require.NoError(t, err, "Expected no error for PostExportsMailboxesWithResponse")
	})

	t.Run("PostExportsMailboxesCsvWithBodyWithResponse", func(t *testing.T) {
		_, err := client.PostExportsMailboxesCsvWithBodyWithResponse(ctx, "test", strings.NewReader(`{}`))
		require.NoError(t, err, "Expected no error for PostExportsMailboxesCsvWithBodyWithResponse")
	})

	t.Run("PostExportsMailboxesCsvWithResponse", func(t *testing.T) {
		_, err := client.PostExportsMailboxesCsvWithResponse(ctx, mailpool.PostExportsMailboxesCsvJSONRequestBody{})
		require.NoError(t, err, "Expected no error for PostExportsMailboxesCsvWithResponse")
	})

	t.Run("GetInboxPlacementsWithResponse", func(t *testing.T) {
		_, err := client.GetInboxPlacementsWithResponse(ctx, &mailpool.GetInboxPlacementsParams{})
		require.NoError(t, err, "Expected no error for GetInboxPlacementsWithResponse")
	})

	t.Run("PostInboxPlacementsWithBodyWithResponse", func(t *testing.T) {
		_, err := client.PostInboxPlacementsWithBodyWithResponse(ctx, "test", strings.NewReader(`{}`))
		require.NoError(t, err, "Expected no error for PostInboxPlacementsWithBodyWithResponse")
	})

	t.Run("PostInboxPlacementsWithResponse", func(t *testing.T) {
		_, err := client.PostInboxPlacementsWithResponse(ctx, mailpool.PostInboxPlacementsJSONRequestBody{})
		require.NoError(t, err, "Expected no error for PostInboxPlacementsWithResponse")
	})

	t.Run("DeleteInboxPlacementsInboxPlacementIdWithResponse", func(t *testing.T) {
		_, err := client.DeleteInboxPlacementsInboxPlacementIdWithResponse(ctx, "test")
		require.NoError(t, err, "Expected no error for DeleteInboxPlacementsInboxPlacementIdWithResponse")
	})

	t.Run("GetInboxPlacementsInboxPlacementIdWithResponse", func(t *testing.T) {
		_, err := client.GetInboxPlacementsInboxPlacementIdWithResponse(ctx, "test")
		require.NoError(t, err, "Expected no error for GetInboxPlacementsInboxPlacementIdWithResponse")
	})

	t.Run("PostInboxPlacementsInboxPlacementIdRunWithResponse", func(t *testing.T) {
		_, err := client.PostInboxPlacementsInboxPlacementIdRunWithResponse(ctx, "test")
		require.NoError(t, err, "Expected no error for PostInboxPlacementsInboxPlacementIdRunWithResponse")
	})

	t.Run("GetIpBlacklistChecksWithResponse", func(t *testing.T) {
		_, err := client.GetIpBlacklistChecksWithResponse(ctx, &mailpool.GetIpBlacklistChecksParams{})
		require.NoError(t, err, "Expected no error for GetIpBlacklistChecksWithResponse")
	})

	t.Run("PostIpBlacklistChecksWithBodyWithResponse", func(t *testing.T) {
		_, err := client.PostIpBlacklistChecksWithBodyWithResponse(ctx, "test", strings.NewReader(`{}`))
		require.NoError(t, err, "Expected no error for PostIpBlacklistChecksWithBodyWithResponse")
	})

	t.Run("PostIpBlacklistChecksWithResponse", func(t *testing.T) {
		_, err := client.PostIpBlacklistChecksWithResponse(ctx, mailpool.PostIpBlacklistChecksJSONRequestBody{})
		require.NoError(t, err, "Expected no error for PostIpBlacklistChecksWithResponse")
	})

	t.Run("DeleteIpBlacklistChecksIdWithResponse", func(t *testing.T) {
		_, err := client.DeleteIpBlacklistChecksIdWithResponse(ctx, 1)
		require.NoError(t, err, "Expected no error for DeleteIpBlacklistChecksIdWithResponse")
	})

	t.Run("GetIpBlacklistChecksIdWithResponse", func(t *testing.T) {
		_, err := client.GetIpBlacklistChecksIdWithResponse(ctx, 1)
		require.NoError(t, err, "Expected no error for GetIpBlacklistChecksIdWithResponse")
	})

	t.Run("GetMailboxesWithResponse", func(t *testing.T) {
		_, err := client.GetMailboxesWithResponse(ctx, &mailpool.GetMailboxesParams{})
		require.NoError(t, err, "Expected no error for GetMailboxesWithResponse")
	})

	t.Run("PostMailboxesWithBodyWithResponse", func(t *testing.T) {
		_, err := client.PostMailboxesWithBodyWithResponse(ctx, "test", strings.NewReader(`{}`))
		require.NoError(t, err, "Expected no error for PostMailboxesWithBodyWithResponse")
	})

	t.Run("PostMailboxesWithResponse", func(t *testing.T) {
		_, err := client.PostMailboxesWithResponse(ctx, mailpool.PostMailboxesJSONRequestBody{})
		require.NoError(t, err, "Expected no error for PostMailboxesWithResponse")
	})

	t.Run("PostMailboxesBulkDeleteImmediatelyWithBodyWithResponse", func(t *testing.T) {
		_, err := client.PostMailboxesBulkDeleteImmediatelyWithBodyWithResponse(ctx, "test", strings.NewReader(`{}`))
		require.NoError(t, err, "Expected no error for PostMailboxesBulkDeleteImmediatelyWithBodyWithResponse")
	})

	t.Run("PostMailboxesBulkDeleteImmediatelyWithResponse", func(t *testing.T) {
		_, err := client.PostMailboxesBulkDeleteImmediatelyWithResponse(ctx, mailpool.PostMailboxesBulkDeleteImmediatelyJSONRequestBody{})
		require.NoError(t, err, "Expected no error for PostMailboxesBulkDeleteImmediatelyWithResponse")
	})

	t.Run("DeleteMailboxesIdWithResponse", func(t *testing.T) {
		_, err := client.DeleteMailboxesIdWithResponse(ctx, "test")
		require.NoError(t, err, "Expected no error for DeleteMailboxesIdWithResponse")
	})

	t.Run("GetMailboxesIdWithResponse", func(t *testing.T) {
		_, err := client.GetMailboxesIdWithResponse(ctx, "test")
		require.NoError(t, err, "Expected no error for GetMailboxesIdWithResponse")
	})

	t.Run("PutMailboxesIdWithBodyWithResponse", func(t *testing.T) {
		_, err := client.PutMailboxesIdWithBodyWithResponse(ctx, "test", "test", strings.NewReader(`{}`))
		require.NoError(t, err, "Expected no error for PutMailboxesIdWithBodyWithResponse")
	})

	t.Run("PutMailboxesIdWithResponse", func(t *testing.T) {
		_, err := client.PutMailboxesIdWithResponse(ctx, "test", mailpool.PutMailboxesIdJSONRequestBody{})
		require.NoError(t, err, "Expected no error for PutMailboxesIdWithResponse")
	})

	t.Run("PostMailboxesMailboxIdCancelDeletionWithResponse", func(t *testing.T) {
		_, err := client.PostMailboxesMailboxIdCancelDeletionWithResponse(ctx, "test")
		require.NoError(t, err, "Expected no error for PostMailboxesMailboxIdCancelDeletionWithResponse")
	})

	t.Run("GetSpamChecksWithResponse", func(t *testing.T) {
		_, err := client.GetSpamChecksWithResponse(ctx, &mailpool.GetSpamChecksParams{})
		require.NoError(t, err, "Expected no error for GetSpamChecksWithResponse")
	})

	t.Run("PostSpamChecksWithBodyWithResponse", func(t *testing.T) {
		_, err := client.PostSpamChecksWithBodyWithResponse(ctx, "test", strings.NewReader(`{}`))
		require.NoError(t, err, "Expected no error for PostSpamChecksWithBodyWithResponse")
	})

	t.Run("PostSpamChecksWithResponse", func(t *testing.T) {
		_, err := client.PostSpamChecksWithResponse(ctx, mailpool.PostSpamChecksJSONRequestBody{})
		require.NoError(t, err, "Expected no error for PostSpamChecksWithResponse")
	})

	t.Run("DeleteSpamChecksSpamCheckIdWithResponse", func(t *testing.T) {
		_, err := client.DeleteSpamChecksSpamCheckIdWithResponse(ctx, "test")
		require.NoError(t, err, "Expected no error for DeleteSpamChecksSpamCheckIdWithResponse")
	})

	t.Run("GetSpamChecksSpamCheckIdWithResponse", func(t *testing.T) {
		_, err := client.GetSpamChecksSpamCheckIdWithResponse(ctx, "test")
		require.NoError(t, err, "Expected no error for GetSpamChecksSpamCheckIdWithResponse")
	})

	t.Run("PostSubscriptionsDecreaseSlotsWithBodyWithResponse", func(t *testing.T) {
		_, err := client.PostSubscriptionsDecreaseSlotsWithBodyWithResponse(ctx, "test", strings.NewReader(`{}`))
		require.NoError(t, err, "Expected no error for PostSubscriptionsDecreaseSlotsWithBodyWithResponse")
	})

	t.Run("PostSubscriptionsDecreaseSlotsWithResponse", func(t *testing.T) {
		_, err := client.PostSubscriptionsDecreaseSlotsWithResponse(ctx, mailpool.PostSubscriptionsDecreaseSlotsJSONRequestBody{})
		require.NoError(t, err, "Expected no error for PostSubscriptionsDecreaseSlotsWithResponse")
	})

	t.Run("PostSubscriptionsIncreaseSlotsWithBodyWithResponse", func(t *testing.T) {
		_, err := client.PostSubscriptionsIncreaseSlotsWithBodyWithResponse(ctx, "test", strings.NewReader(`{}`))
		require.NoError(t, err, "Expected no error for PostSubscriptionsIncreaseSlotsWithBodyWithResponse")
	})

	t.Run("PostSubscriptionsIncreaseSlotsWithResponse", func(t *testing.T) {
		_, err := client.PostSubscriptionsIncreaseSlotsWithResponse(ctx, mailpool.PostSubscriptionsIncreaseSlotsJSONRequestBody{})
		require.NoError(t, err, "Expected no error for PostSubscriptionsIncreaseSlotsWithResponse")
	})

	t.Run("GetSubscriptionsSlotsWithBodyWithResponse", func(t *testing.T) {
		_, err := client.GetSubscriptionsSlotsWithBodyWithResponse(ctx, "test", strings.NewReader(`{}`))
		require.NoError(t, err, "Expected no error for GetSubscriptionsSlotsWithBodyWithResponse")
	})

	t.Run("PostSubscriptionsUpdateSlotsWithBodyWithResponse", func(t *testing.T) {
		_, err := client.PostSubscriptionsUpdateSlotsWithBodyWithResponse(ctx, "test", strings.NewReader(`{}`))
		require.NoError(t, err, "Expected no error for PostSubscriptionsUpdateSlotsWithBodyWithResponse")
	})

	t.Run("PostSubscriptionsUpdateSlotsWithResponse", func(t *testing.T) {
		_, err := client.PostSubscriptionsUpdateSlotsWithResponse(ctx, mailpool.PostSubscriptionsUpdateSlotsJSONRequestBody{})
		require.NoError(t, err, "Expected no error for PostSubscriptionsUpdateSlotsWithResponse")
	})

}
