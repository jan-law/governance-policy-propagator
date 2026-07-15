package controllers

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	appsv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/placementrule/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"

	policyv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	policyv1beta1 "open-cluster-management.io/governance-policy-propagator/api/v1beta1"
)

const (
	policySetTestNamespace = "policies"
	policySetTestName      = "test-set"
	policySetTestBinding   = "test-pb"
	policySetTestPLR       = "test-plr"
)

func newPolicySetTestScheme(t *testing.T) *k8sruntime.Scheme {
	t.Helper()

	scheme := k8sruntime.NewScheme()
	if err := policyv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add policy v1 scheme: %v", err)
	}

	if err := policyv1beta1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add policy v1beta1 scheme: %v", err)
	}

	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add placementrule scheme: %v", err)
	}

	return scheme
}

func newPolicySetTestClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()

	return fake.NewClientBuilder().
		WithScheme(newPolicySetTestScheme(t)).
		WithObjects(objs...).
		WithStatusSubresource(&policyv1beta1.PolicySet{}, &policyv1.Policy{}).
		Build()
}

func testPolicySetWithoutExclusions(policies ...policyv1beta1.NonEmptyString) *policyv1beta1.PolicySet {
	return &policyv1beta1.PolicySet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      policySetTestName,
			Namespace: policySetTestNamespace,
		},
		Spec: policyv1beta1.PolicySetSpec{
			Policies: policies,
		},
	}
}

func testPolicySetPlacementRule(clusters ...string) *appsv1.PlacementRule {
	decisions := make([]appsv1.PlacementDecision, len(clusters))
	for i, cluster := range clusters {
		decisions[i] = appsv1.PlacementDecision{
			ClusterName:      cluster,
			ClusterNamespace: cluster,
		}
	}

	return &appsv1.PlacementRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      policySetTestPLR,
			Namespace: policySetTestNamespace,
		},
		Status: appsv1.PlacementRuleStatus{Decisions: decisions},
	}
}

func testPolicySetPlacementBinding() *policyv1.PlacementBinding {
	return &policyv1.PlacementBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      policySetTestBinding,
			Namespace: policySetTestNamespace,
		},
		Subjects: []policyv1.Subject{
			{
				APIGroup: policyv1.SchemeGroupVersion.Group,
				Kind:     policyv1.PolicySetKind,
				Name:     policySetTestName,
			},
		},
		PlacementRef: policyv1.PlacementSubject{
			APIGroup: appsv1.SchemeGroupVersion.Group,
			Kind:     "PlacementRule",
			Name:     policySetTestPLR,
		},
	}
}

func testChildPolicy(name string, mutate func(*policyv1.Policy)) *policyv1.Policy {
	policy := &policyv1.Policy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: policySetTestNamespace,
		},
		Status: policyv1.PolicyStatus{
			Placement: []*policyv1.Placement{
				{
					PlacementBinding: policySetTestBinding,
					PlacementRule:    policySetTestPLR,
					PolicySet:        policySetTestName,
				},
			},
		},
	}

	if mutate != nil {
		mutate(policy)
	}

	return policy
}

func runProcessPolicySet(
	t *testing.T, c client.Client, policySet *policyv1beta1.PolicySet,
) policyv1beta1.PolicySetStatus {
	t.Helper()

	reconciler := &PolicySetReconciler{
		Client:   c,
		Recorder: events.NewFakeRecorder(100),
	}

	needsUpdate := reconciler.processPolicySet(context.Background(), log.Log, policySet)
	if !needsUpdate {
		return policySet.Status
	}

	return policySet.Status
}

func TestProcessPolicySetWithoutExclusionsEmptyPolicies(t *testing.T) {
	t.Parallel()

	policySet := testPolicySetWithoutExclusions()
	policySet.Status = policyv1beta1.PolicySetStatus{
		StatusMessage: "stale",
		Compliant:     policyv1.Compliant,
	}

	c := newPolicySetTestClient(t, policySet)
	status := runProcessPolicySet(t, c, policySet)

	expected := policyv1beta1.PolicySetStatus{}
	if !cmp.Equal(status, expected) {
		t.Fatalf("expected empty status, got %+v", status)
	}
}

func TestProcessPolicySetWithoutExclusionsAllCompliant(t *testing.T) {
	t.Parallel()

	policySet := testPolicySetWithoutExclusions("policy-a", "policy-b")
	policyA := testChildPolicy("policy-a", func(p *policyv1.Policy) {
		p.Status.Status = []*policyv1.CompliancePerClusterStatus{
			{ClusterName: "managed1", ComplianceState: policyv1.Compliant},
			{ClusterName: "managed2", ComplianceState: policyv1.Compliant},
		}
	})
	policyB := testChildPolicy("policy-b", func(p *policyv1.Policy) {
		p.Status.Status = []*policyv1.CompliancePerClusterStatus{
			{ClusterName: "managed1", ComplianceState: policyv1.Compliant},
			{ClusterName: "managed2", ComplianceState: policyv1.Compliant},
		}
	})

	c := newPolicySetTestClient(t,
		policySet,
		policyA,
		policyB,
		testPolicySetPlacementRule("managed1", "managed2"),
		testPolicySetPlacementBinding(),
	)

	status := runProcessPolicySet(t, c, policySet)

	expected := policyv1beta1.PolicySetStatus{
		Placement: []policyv1beta1.PolicySetStatusPlacement{
			{
				PlacementBinding: policySetTestBinding,
				PlacementRule:    policySetTestPLR,
			},
		},
		Compliant:     policyv1.Compliant,
		StatusMessage: "All policies are reporting status",
	}
	if !cmp.Equal(status, expected) {
		t.Fatalf("expected %+v, got %+v", expected, status)
	}
}

func TestProcessPolicySetWithoutExclusionsDisabledPolicy(t *testing.T) {
	t.Parallel()

	policySet := testPolicySetWithoutExclusions("policy-a")
	policyA := testChildPolicy("policy-a", func(p *policyv1.Policy) {
		p.Spec.Disabled = true
	})

	c := newPolicySetTestClient(t, policySet, policyA)
	status := runProcessPolicySet(t, c, policySet)

	expected := policyv1beta1.PolicySetStatus{
		Placement: []policyv1beta1.PolicySetStatusPlacement{
			{
				PlacementBinding: policySetTestBinding,
				PlacementRule:    policySetTestPLR,
			},
		},
		StatusMessage: "Disabled policies: policy-a",
	}
	if !cmp.Equal(status, expected) {
		t.Fatalf("expected %+v, got %+v", expected, status)
	}
}

func TestProcessPolicySetWithoutExclusionsDeletedPolicy(t *testing.T) {
	t.Parallel()

	policySet := testPolicySetWithoutExclusions("policy-a")
	c := newPolicySetTestClient(t, policySet)
	status := runProcessPolicySet(t, c, policySet)

	if status.StatusMessage != "Deleted policies: policy-a" {
		t.Fatalf("expected deleted policy message, got %q", status.StatusMessage)
	}

	if status.Compliant != "" {
		t.Fatalf("expected no compliance state, got %q", status.Compliant)
	}
}

func TestProcessPolicySetWithoutExclusionsUnknownCompliance(t *testing.T) {
	t.Parallel()

	policySet := testPolicySetWithoutExclusions("policy-a")
	policyA := testChildPolicy("policy-a", func(p *policyv1.Policy) {
		p.Status.Status = []*policyv1.CompliancePerClusterStatus{
			{ClusterName: "other-cluster", ComplianceState: policyv1.Compliant},
		}
	})

	c := newPolicySetTestClient(t,
		policySet,
		policyA,
		testPolicySetPlacementRule("managed1"),
		testPolicySetPlacementBinding(),
	)
	status := runProcessPolicySet(t, c, policySet)

	expected := policyv1beta1.PolicySetStatus{
		Placement: []policyv1beta1.PolicySetStatusPlacement{
			{
				PlacementBinding: policySetTestBinding,
				PlacementRule:    policySetTestPLR,
			},
		},
		StatusMessage: "No status provided while awaiting policy status: policy-a",
	}
	if !cmp.Equal(status, expected) {
		t.Fatalf("expected %+v, got %+v", expected, status)
	}
}

func TestProcessPolicySetWithoutExclusionsPendingAndNonCompliant(t *testing.T) {
	t.Parallel()

	policySet := testPolicySetWithoutExclusions("policy-a", "policy-b")
	policyA := testChildPolicy("policy-a", func(p *policyv1.Policy) {
		p.Status.Status = []*policyv1.CompliancePerClusterStatus{
			{ClusterName: "managed1", ComplianceState: policyv1.Pending},
		}
	})
	policyB := testChildPolicy("policy-b", func(p *policyv1.Policy) {
		p.Status.Status = []*policyv1.CompliancePerClusterStatus{
			{ClusterName: "managed1", ComplianceState: policyv1.NonCompliant},
		}
	})

	c := newPolicySetTestClient(t,
		policySet,
		policyA,
		policyB,
		testPolicySetPlacementRule("managed1"),
		testPolicySetPlacementBinding(),
	)
	status := runProcessPolicySet(t, c, policySet)

	expected := policyv1beta1.PolicySetStatus{
		Placement: []policyv1beta1.PolicySetStatusPlacement{
			{
				PlacementBinding: policySetTestBinding,
				PlacementRule:    policySetTestPLR,
			},
		},
		Compliant: policyv1.NonCompliant,
		StatusMessage: "Policies awaiting pending dependencies: policy-a",
	}
	if !cmp.Equal(status, expected) {
		t.Fatalf("expected %+v, got %+v", expected, status)
	}
}

func TestProcessPolicySetWithoutExclusionsSharedPlacementBindingCache(t *testing.T) {
	t.Parallel()

	policySet := testPolicySetWithoutExclusions("policy-a", "policy-b")
	policyA := testChildPolicy("policy-a", func(p *policyv1.Policy) {
		p.Status.Status = []*policyv1.CompliancePerClusterStatus{
			{ClusterName: "managed1", ComplianceState: policyv1.Compliant},
			{ClusterName: "managed2", ComplianceState: policyv1.Compliant},
		}
	})
	policyB := testChildPolicy("policy-b", func(p *policyv1.Policy) {
		p.Status.Status = []*policyv1.CompliancePerClusterStatus{
			{ClusterName: "managed1", ComplianceState: policyv1.Compliant},
			{ClusterName: "managed2", ComplianceState: policyv1.Compliant},
		}
	})

	c := newPolicySetTestClient(t,
		policySet,
		policyA,
		policyB,
		testPolicySetPlacementRule("managed1", "managed2"),
		testPolicySetPlacementBinding(),
	)
	status := runProcessPolicySet(t, c, policySet)

	if status.Compliant != policyv1.Compliant {
		t.Fatalf("expected compliant status, got %q", status.Compliant)
	}

	if status.StatusMessage != "All policies are reporting status" {
		t.Fatalf("expected all reporting status, got %q", status.StatusMessage)
	}
}

func TestProcessPolicySetWithoutExclusionsMissingPlacementBinding(t *testing.T) {
	t.Parallel()

	policySet := testPolicySetWithoutExclusions("policy-a", "policy-b")
	policyA := testChildPolicy("policy-a", nil)
	policyB := testChildPolicy("policy-b", nil)

	c := newPolicySetTestClient(t, policySet, policyA, policyB)
	status := runProcessPolicySet(t, c, policySet)

	expected := policyv1beta1.PolicySetStatus{
		Placement: []policyv1beta1.PolicySetStatusPlacement{
			{
				PlacementBinding: policySetTestBinding,
				PlacementRule:    policySetTestPLR,
			},
		},
		StatusMessage: "No status provided while awaiting policy status: policy-a, policy-b",
	}
	if !cmp.Equal(status, expected) {
		t.Fatalf("expected %+v, got %+v", expected, status)
	}
}

func TestGetStatusMessageWithoutExclusionsMatchesMain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		args     func() (disabled, clusterExcluded, invalidExclusion, unknown, deleted, pending []string)
		expected string
	}{
		{
			name:     "all reporting",
			args:     func() ([]string, []string, []string, []string, []string, []string) { return nil, nil, nil, nil, nil, nil },
			expected: "All policies are reporting status",
		},
		{
			name: "disabled only",
			args: func() ([]string, []string, []string, []string, []string, []string) {
				return []string{"policy-a"}, nil, nil, nil, nil, nil
			},
			expected: "Disabled policies: policy-a",
		},
		{
			name: "deleted only",
			args: func() ([]string, []string, []string, []string, []string, []string) {
				return nil, nil, nil, nil, []string{"policy-a"}, nil
			},
			expected: "Deleted policies: policy-a",
		},
		{
			name: "pending before unknown like main ordering",
			args: func() ([]string, []string, []string, []string, []string, []string) {
				return nil, nil, nil, []string{"policy-b"}, nil, []string{"policy-a"}
			},
			expected: "Policies awaiting pending dependencies: policy-a; No status provided while awaiting policy status: policy-b",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			disabled, clusterExcluded, invalidExclusion, unknown, deleted, pending := tt.args()
			got := getStatusMessage(disabled, clusterExcluded, invalidExclusion, unknown, deleted, pending)
			if got != tt.expected {
				t.Fatalf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}
