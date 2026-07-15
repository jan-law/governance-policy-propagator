package common

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	policiesv1beta1 "open-cluster-management.io/governance-policy-propagator/api/v1beta1"
)

func TestPolicySetUpdateDiffWithoutExclusionsMatchesMain(t *testing.T) {
	t.Parallel()

	base := &policiesv1beta1.PolicySet{
		ObjectMeta: metav1.ObjectMeta{Name: "set-a", Namespace: "policies"},
		Spec: policiesv1beta1.PolicySetSpec{
			Policies: []policiesv1beta1.NonEmptyString{"policy-a", "policy-b"},
		},
	}

	tests := []struct {
		name     string
		old      *policiesv1beta1.PolicySet
		new      *policiesv1beta1.PolicySet
		expected []string
	}{
		{
			name:     "unchanged policies",
			old:      base.DeepCopy(),
			new:      base.DeepCopy(),
			expected: nil,
		},
		{
			name: "added policy",
			old:  base.DeepCopy(),
			new: func() *policiesv1beta1.PolicySet {
				updated := base.DeepCopy()
				updated.Spec.Policies = append(updated.Spec.Policies, "policy-c")

				return updated
			}(),
			expected: []string{"policy-c"},
		},
		{
			name: "removed policy",
			old:  base.DeepCopy(),
			new: func() *policiesv1beta1.PolicySet {
				updated := base.DeepCopy()
				updated.Spec.Policies = []policiesv1beta1.NonEmptyString{"policy-a"}

				return updated
			}(),
			expected: []string{"policy-b"},
		},
		{
			name: "added and removed policy",
			old:  base.DeepCopy(),
			new: func() *policiesv1beta1.PolicySet {
				updated := base.DeepCopy()
				updated.Spec.Policies = []policiesv1beta1.NonEmptyString{"policy-a", "policy-c"}

				return updated
			}(),
			expected: []string{"policy-c", "policy-b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := policySetUpdateDiff(tt.old, tt.new)
			if !sameStringSet(got, tt.expected) {
				t.Fatalf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}

func TestFilterExcludedPolicySetClustersForListedPolicyNoExclusionsIsIdentity(t *testing.T) {
	t.Parallel()

	policySet := &policiesv1beta1.PolicySet{
		Spec: policiesv1beta1.PolicySetSpec{
			Policies: []policiesv1beta1.NonEmptyString{"policy-a"},
		},
	}
	input := []string{"managed1", "managed2", "managed3"}

	got := FilterExcludedPolicySetClustersForListedPolicy(policySet, "policy-a", input)
	if !cmp.Equal(got, input) {
		t.Fatalf("expected unchanged clusters %v, got %v", input, got)
	}
}

func TestSummarizePolicySetExclusionsEmptySpecMatchesMain(t *testing.T) {
	t.Parallel()

	policySet := &policiesv1beta1.PolicySet{
		Spec: policiesv1beta1.PolicySetSpec{
			Policies: []policiesv1beta1.NonEmptyString{"policy-a"},
		},
	}

	summary := SummarizePolicySetExclusions(policySet)
	if len(summary.InvalidPolicyNames) != 0 {
		t.Fatalf("expected no invalid policies, got %v", summary.InvalidPolicyNames)
	}

	if len(summary.ValidExclusions) != 0 {
		t.Fatalf("expected no valid exclusions, got %v", summary.ValidExclusions)
	}

	if summary.StatusExclusions != nil {
		t.Fatalf("expected nil status exclusions, got %v", summary.StatusExclusions)
	}

	if len(summary.ClusterExcludedMessages) != 0 {
		t.Fatalf("expected no status messages, got %v", summary.ClusterExcludedMessages)
	}
}

func sameStringSet(got, expected []string) bool {
	if len(got) != len(expected) {
		return false
	}

	seen := make(map[string]struct{}, len(got))
	for _, item := range got {
		seen[item] = struct{}{}
	}

	for _, item := range expected {
		if _, ok := seen[item]; !ok {
			return false
		}
	}

	return true
}
