package workers

import (
	"testing"

	"github.com/Yankzy/usetoro/tap/pkg/core"
)

func TestBankReconciliationWorkersUseDirectWorkerInboxes(t *testing.T) {
	workers := []Worker{&BankReconciliationLifecycleWorker{}, &ReconciliationMatchingWorker{}}
	for _, worker := range workers {
		subscriptions := worker.Subscriptions()
		if len(subscriptions) == 0 {
			t.Fatalf("%T has no subscriptions", worker)
		}
		for _, subscription := range subscriptions {
			if subscription.Subject == "" {
				t.Fatalf("%T registered an empty subject", worker)
			}
		}
	}
	for _, activity := range []string{
		reconciliationBaselineActivity, reconciliationPrepareActivity, reconciliationReviseActivity,
		reconciliationCloseActivity, reconciliationCorrectActivity, reconciliationGenerateMatchesActivity,
		reconciliationManualMatchActivity, reconciliationConfirmMatchActivity, reconciliationResolveReviewActivity,
	} {
		if _, err := core.BuildWorkerInboxFromActivity(activity); err != nil {
			t.Fatalf("activity %q is not directly routable: %v", activity, err)
		}
	}
}

func TestStatementIntakeRetryIdentityAndHoldCodes(t *testing.T) {
	documentsA := []string{"doc-b", "doc-a"}
	documentsB := []string{"doc-a", "doc-b"}
	if statementIntakeKey("rap_atlas_sarl", "bank-1", documentsA) != statementIntakeKey("rap_atlas_sarl", "bank-1", documentsB) {
		t.Fatal("statement intake key must be document-order independent")
	}
	if pcmHoldCode(assertionError("pcm: HOLD_STATEMENT_METADATA: opening balance missing")) != "HOLD_STATEMENT_METADATA" {
		t.Fatal("expected structured PCM hold code")
	}
}

type assertionError string

func (e assertionError) Error() string { return string(e) }
