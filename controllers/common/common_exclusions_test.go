package common

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	appsv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/placementrule/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	policiesv1beta1 "open-cluster-management.io/governance-policy-propagator/api/v1beta1"
)

const (
	exclusionTestNamespace = "policies"
	exclusionTestPolicy    = "policy-a"
	exclusionTestPolicySet = "policyset-a"
)

func newExclusionTestScheme(t *testing.T) *k8sruntime.Scheme {
	t.Helper()

	scheme := k8sruntime.NewScheme()
	if err := policiesv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add policy v1 scheme: %v", err)
	}

	if err := policiesv1beta1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add policy v1beta1 scheme: %v", err)
	}

	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add placementrule scheme: %v", err)
	}

	return scheme
}

func testPlacementRule(name string, clusters ...string) *appsv1.PlacementRule {
	decisions := make([]appsv1.PlacementDecision, len(clusters))
	for i, cluster := range clusters {
		decisions[i] = appsv1.PlacementDecision{
			ClusterName:      cluster,
			ClusterNamespace: cluster,
		}
	}

	return &appsv1.PlacementRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: exclusionTestNamespace,
		},
		Status: appsv1.PlacementRuleStatus{Decisions: decisions},
	}
}

func testPlacementBinding(name, subjectKind, subjectName, plrName string) *policiesv1.PlacementBinding {
	return &policiesv1.PlacementBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: exclusionTestNamespace,
		},
		Subjects: []policiesv1.Subject{
			{
				APIGroup: policiesv1.SchemeGroupVersion.Group,
				Kind:     subjectKind,
				Name:     subjectName,
			},
		},
		PlacementRef: policiesv1.PlacementSubject{
			APIGroup: appsv1.SchemeGroupVersion.Group,
			Kind:     "PlacementRule",
			Name:     plrName,
		},
	}
}

func testPolicySet(exclusions []policiesv1beta1.PolicySetExclusion) *policiesv1beta1.PolicySet {
	return &policiesv1beta1.PolicySet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      exclusionTestPolicySet,
			Namespace: exclusionTestNamespace,
		},
		Spec: policiesv1beta1.PolicySetSpec{
			Policies:   []policiesv1beta1.NonEmptyString{exclusionTestPolicy},
			Exclusions: exclusions,
		},
	}
}

func testRootPolicy() *policiesv1.Policy {
	return &policiesv1.Policy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      exclusionTestPolicy,
			Namespace: exclusionTestNamespace,
		},
	}
}

func newExclusionTestClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()

	scheme := newExclusionTestScheme(t)

	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

func TestIsPolicyExcludedForClusterInPolicySet(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		policyName  string
		clusterName string
		expected    bool
	}{
		{
			name:        "excluded cluster",
			policyName:  exclusionTestPolicy,
			clusterName: "managed1",
			expected:    true,
		},
		{
			name:        "non-excluded cluster",
			policyName:  exclusionTestPolicy,
			clusterName: "managed3",
			expected:    false,
		},
		{
			name:        "different policy",
			policyName:  "policy-b",
			clusterName: "managed1",
			expected:    false,
		},
	}

	policySet := testPolicySet([]policiesv1beta1.PolicySetExclusion{
		{
			PolicyName:   exclusionTestPolicy,
			ClusterNames: []policiesv1beta1.NonEmptyString{"managed1", "managed2"},
		},
	})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := IsPolicyExcludedForClusterInPolicySet(policySet, tt.policyName, tt.clusterName)
			if got != tt.expected {
				t.Fatalf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}

func TestFilterExcludedPolicySetClusters(t *testing.T) {
	t.Parallel()

	policySet := testPolicySet([]policiesv1beta1.PolicySetExclusion{
		{
			PolicyName:   exclusionTestPolicy,
			ClusterNames: []policiesv1beta1.NonEmptyString{"managed2"},
		},
	})

	tests := []struct {
		name     string
		policy   string
		input    []string
		expected []string
	}{
		{
			name:     "filters excluded cluster",
			policy:   exclusionTestPolicy,
			input:    []string{"managed1", "managed2", "managed3"},
			expected: []string{"managed1", "managed3"},
		},
		{
			name:     "no exclusions for other policy",
			policy:   "policy-b",
			input:    []string{"managed1", "managed2"},
			expected: []string{"managed1", "managed2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := FilterExcludedPolicySetClusters(policySet, tt.policy, tt.input)
			if !cmp.Equal(got, tt.expected) {
				t.Fatalf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}

func TestBuildPolicySetStatusExclusions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    []policiesv1beta1.PolicySetExclusion
		expected []policiesv1beta1.PolicySetStatusExclusion
	}{
		{
			name: "deduplicates clusters per policy",
			input: []policiesv1beta1.PolicySetExclusion{
				{
					PolicyName:   exclusionTestPolicy,
					ClusterNames: []policiesv1beta1.NonEmptyString{"managed2", "managed1"},
				},
				{
					PolicyName:   exclusionTestPolicy,
					ClusterNames: []policiesv1beta1.NonEmptyString{"managed1"},
				},
			},
			expected: []policiesv1beta1.PolicySetStatusExclusion{
				{
					PolicyName: exclusionTestPolicy,
					Clusters:   []policiesv1beta1.NonEmptyString{"managed1", "managed2"},
				},
			},
		},
		{
			name:     "empty input",
			input:    nil,
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := BuildPolicySetStatusExclusions(tt.input)
			if !cmp.Equal(got, tt.expected) {
				t.Fatalf("expected %+v, got %+v", tt.expected, got)
			}
		})
	}
}

func TestBuildPlacementPathExclusions(t *testing.T) {
	t.Parallel()

	policySet := testPolicySet([]policiesv1beta1.PolicySetExclusion{
		{
			PolicyName:   exclusionTestPolicy,
			ClusterNames: []policiesv1beta1.NonEmptyString{"managed2"},
		},
	})

	tests := []struct {
		name     string
		policy   string
		clusters []string
		expected []policiesv1.PolicyExclusion
	}{
		{
			name:     "reports excluded clusters on path",
			policy:   exclusionTestPolicy,
			clusters: []string{"managed1", "managed2"},
			expected: []policiesv1.PolicyExclusion{{ClusterName: "managed2"}},
		},
		{
			name:     "no exclusions for other policy",
			policy:   "policy-b",
			clusters: []string{"managed1", "managed2"},
			expected: []policiesv1.PolicyExclusion{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := BuildPlacementPathExclusions(policySet, tt.policy, tt.clusters)
			if !cmp.Equal(got, tt.expected) {
				t.Fatalf("expected %+v, got %+v", tt.expected, got)
			}
		})
	}
}

func testPolicySetPlacementBinding(plrName string) *policiesv1.PlacementBinding {
	return testPlacementBinding(
		"policyset-pb",
		policiesv1.PolicySetKind,
		exclusionTestPolicySet,
		plrName,
	)
}

func TestGetPolicyPlacementDecisionsWithExclusions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	policySet := testPolicySet([]policiesv1beta1.PolicySetExclusion{
		{
			PolicyName:   exclusionTestPolicy,
			ClusterNames: []policiesv1beta1.NonEmptyString{"managed2"},
		},
	})
	policySetPLR := testPlacementRule("policyset-plr", "managed1", "managed2")
	directPLR := testPlacementRule("direct-plr", "managed2")
	policySetPB := testPolicySetPlacementBinding("policyset-plr")
	directPB := testPlacementBinding("direct-pb", policiesv1.Kind, exclusionTestPolicy, "direct-plr")

	c := newExclusionTestClient(t,
		testRootPolicy(),
		policySet,
		policySetPLR,
		directPLR,
		policySetPB,
		directPB,
	)

	tests := []struct {
		name               string
		pb                 *policiesv1.PlacementBinding
		expectedDecisions  []string
		expectedExclusions []policiesv1.PolicyExclusion
	}{
		{
			name:              "policyset path filters excluded cluster",
			pb:                policySetPB,
			expectedDecisions: []string{"managed1"},
			expectedExclusions: []policiesv1.PolicyExclusion{
				{ClusterName: "managed2"},
			},
		},
		{
			name:               "direct binding is unaffected",
			pb:                 directPB,
			expectedDecisions:  []string{"managed2"},
			expectedExclusions: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			decisions, placements, err := GetPolicyPlacementDecisions(ctx, c, testRootPolicy(), tt.pb)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !cmp.Equal(decisions, tt.expectedDecisions) {
				t.Fatalf("expected decisions %v, got %v", tt.expectedDecisions, decisions)
			}

			if len(placements) != 1 {
				t.Fatalf("expected one placement, got %d", len(placements))
			}

			if !cmp.Equal(placements[0].Exclusions, tt.expectedExclusions) {
				t.Fatalf("expected exclusions %+v, got %+v", tt.expectedExclusions, placements[0].Exclusions)
			}
		})
	}
}

func TestComputeRemainingBindings(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	policySet := testPolicySet([]policiesv1beta1.PolicySetExclusion{
		{
			PolicyName:   exclusionTestPolicy,
			ClusterNames: []policiesv1beta1.NonEmptyString{"managed2"},
		},
	})
	policySetPLR := testPlacementRule("policyset-plr", "managed1", "managed2")
	directPLR := testPlacementRule("direct-plr", "managed2")
	policySetPB := testPolicySetPlacementBinding("policyset-plr")
	directPB := testPlacementBinding("direct-pb", policiesv1.Kind, exclusionTestPolicy, "direct-plr")

	baseObjects := []client.Object{
		testRootPolicy(),
		policySet,
		policySetPLR,
		directPLR,
		policySetPB,
		directPB,
	}

	tests := []struct {
		name      string
		objects   []client.Object
		policy    *policiesv1.Policy
		decisions DecisionSet
		expected  map[string][]policiesv1.RemainingBinding
	}{
		{
			name:    "reports alternate binding when policyset path excludes cluster",
			objects: baseObjects,
			policy:  testRootPolicy(),
			decisions: DecisionSet{
				"managed1": true,
				"managed2": true,
			},
			expected: map[string][]policiesv1.RemainingBinding{
				"managed2": {{PlacementBinding: "direct-pb"}},
			},
		},
		{
			name:    "single binding with exclusion has no remaining bindings",
			objects: []client.Object{testRootPolicy(), policySet, policySetPLR, policySetPB},
			policy:  testRootPolicy(),
			decisions: DecisionSet{
				"managed1": true,
			},
			expected: map[string][]policiesv1.RemainingBinding{},
		},
		{
			name: "disabled policy returns nil",
			objects: []client.Object{
				&policiesv1.Policy{
					ObjectMeta: metav1.ObjectMeta{
						Name:      exclusionTestPolicy,
						Namespace: exclusionTestNamespace,
					},
					Spec: policiesv1.PolicySpec{Disabled: true},
				},
				policySet,
				policySetPLR,
				policySetPB,
			},
			policy: &policiesv1.Policy{
				ObjectMeta: metav1.ObjectMeta{
					Name:      exclusionTestPolicy,
					Namespace: exclusionTestNamespace,
				},
				Spec: policiesv1.PolicySpec{Disabled: true},
			},
			decisions: DecisionSet{"managed1": true},
			expected:  nil,
		},
		{
			name:      "empty decisions returns nil",
			objects:   baseObjects,
			policy:    testRootPolicy(),
			decisions: DecisionSet{},
			expected:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := newExclusionTestClient(t, tt.objects...)

			got, err := ComputeRemainingBindings(ctx, c, tt.policy, tt.decisions)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !cmp.Equal(got, tt.expected) {
				t.Fatalf("expected %+v, got %+v", tt.expected, got)
			}
		})
	}
}
