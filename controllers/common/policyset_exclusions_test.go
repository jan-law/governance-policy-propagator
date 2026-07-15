package common

import (
	"testing"

	policiesv1beta1 "open-cluster-management.io/governance-policy-propagator/api/v1beta1"
)

func TestInvalidPolicySetExclusionPolicies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		policySet policiesv1beta1.PolicySet
		expected  []string
	}{
		{
			name: "valid exclusion policy",
			policySet: policiesv1beta1.PolicySet{
				Spec: policiesv1beta1.PolicySetSpec{
					Policies: []policiesv1beta1.NonEmptyString{"policy-a"},
					Exclusions: []policiesv1beta1.PolicySetExclusion{
						{
							PolicyName:   "policy-a",
							ClusterNames: []policiesv1beta1.NonEmptyString{"managed1"},
						},
					},
				},
			},
		},
		{
			name: "invalid exclusion policy",
			policySet: policiesv1beta1.PolicySet{
				Spec: policiesv1beta1.PolicySetSpec{
					Policies: []policiesv1beta1.NonEmptyString{"policy-a"},
					Exclusions: []policiesv1beta1.PolicySetExclusion{
						{
							PolicyName:   "policy-b",
							ClusterNames: []policiesv1beta1.NonEmptyString{"managed1"},
						},
					},
				},
			},
			expected: []string{"policy-b"},
		},
		{
			name: "no exclusions",
			policySet: policiesv1beta1.PolicySet{
				Spec: policiesv1beta1.PolicySetSpec{
					Policies: []policiesv1beta1.NonEmptyString{"policy-a"},
				},
			},
		},
		{
			name: "deduplicates invalid exclusion policies",
			policySet: policiesv1beta1.PolicySet{
				Spec: policiesv1beta1.PolicySetSpec{
					Policies: []policiesv1beta1.NonEmptyString{"policy-a"},
					Exclusions: []policiesv1beta1.PolicySetExclusion{
						{
							PolicyName:   "policy-b",
							ClusterNames: []policiesv1beta1.NonEmptyString{"managed1"},
						},
						{
							PolicyName:   "policy-b",
							ClusterNames: []policiesv1beta1.NonEmptyString{"managed2"},
						},
					},
				},
			},
			expected: []string{"policy-b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := InvalidPolicySetExclusionPolicies(&tt.policySet)
			if len(got) != len(tt.expected) {
				t.Fatalf("expected %v, got %v", tt.expected, got)
			}

			for i := range got {
				if got[i] != tt.expected[i] {
					t.Fatalf("expected %v, got %v", tt.expected, got)
				}
			}
		})
	}
}

func TestFilterValidPolicySetExclusions(t *testing.T) {
	t.Parallel()

	policySet := &policiesv1beta1.PolicySet{
		Spec: policiesv1beta1.PolicySetSpec{
			Policies: []policiesv1beta1.NonEmptyString{"policy-a"},
			Exclusions: []policiesv1beta1.PolicySetExclusion{
				{
					PolicyName:   "policy-a",
					ClusterNames: []policiesv1beta1.NonEmptyString{"managed1"},
				},
				{
					PolicyName:   "policy-b",
					ClusterNames: []policiesv1beta1.NonEmptyString{"managed2"},
				},
			},
		},
	}

	got := FilterValidPolicySetExclusions(policySet)
	if len(got) != 1 {
		t.Fatalf("expected 1 valid exclusion, got %d", len(got))
	}

	if got[0].PolicyName != "policy-a" {
		t.Fatalf("expected policy-a, got %s", got[0].PolicyName)
	}
}

func TestIsPolicyListedInPolicySet(t *testing.T) {
	t.Parallel()

	policySet := &policiesv1beta1.PolicySet{
		Spec: policiesv1beta1.PolicySetSpec{
			Policies: []policiesv1beta1.NonEmptyString{"policy-a"},
		},
	}

	if !IsPolicyListedInPolicySet(policySet, "policy-a") {
		t.Fatal("expected policy-a to be listed")
	}

	if IsPolicyListedInPolicySet(policySet, "policy-b") {
		t.Fatal("expected policy-b to be absent")
	}
}
