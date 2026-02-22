package quickbooks

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHumanReadableReason(t *testing.T) {
	exp := MatchExplanation{
		GroupName:   "Software Subscriptions",
		Logic:       "AND",
		FinalResult: true,
		ConditionExp: []ConditionResult{
			{
				Field:       "description",
				Operator:    "contains",
				TargetValue: "aws",
				Result:      true,
			},
			{
				Field:       "amount",
				Operator:    "gt",
				TargetValue: "50.00",
				Result:      true,
			},
		},
	}

	expected := "Categorized by Rule: Software Subscriptions. Because the description contained 'aws' and the amount was greater than $50.00."
	assert.Equal(t, expected, exp.HumanReadableReason())
}
