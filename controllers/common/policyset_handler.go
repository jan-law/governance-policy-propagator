// Copyright Contributors to the Open Cluster Management project

package common

import (
	"context"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	policiesv1beta1 "open-cluster-management.io/governance-policy-propagator/api/v1beta1"
)

// EnqueueRequestsFromPolicySet adds reconcile requests for every policy in the policy set,
// except on updates, it'll only add the diff between the old and new sets.
type EnqueueRequestsFromPolicySet struct{}

// mapPolicySetToRequests maps a PolicySet to all the Policies in its policies list.
func mapPolicySetToRequests(object client.Object) []reconcile.Request {
	log := log.WithValues("policySetName", object.GetName(), "namespace", object.GetNamespace())
	log.V(2).Info("Reconcile Request for PolicySet")

	var result []reconcile.Request

	//nolint:forcetypeassert
	policySet := object.(*policiesv1beta1.PolicySet)

	for _, plc := range policySet.Spec.Policies {
		policyName := string(plc)
		log.V(2).Info("Found reconciliation request from a policyset", "policyName", policyName)

		request := reconcile.Request{NamespacedName: types.NamespacedName{
			Name:      policyName,
			Namespace: object.GetNamespace(),
		}}
		result = append(result, request)
	}

	return result
}

// Create implements EventHandler
func (e *EnqueueRequestsFromPolicySet) Create(_ context.Context, evt event.CreateEvent,
	q workqueue.TypedRateLimitingInterface[reconcile.Request],
) {
	for _, policy := range mapPolicySetToRequests(evt.Object) {
		q.Add(policy)
	}
}

// Update implements EventHandler
// Enqueues the diff between the new and old policy sets in the UpdateEvent
func (e *EnqueueRequestsFromPolicySet) Update(_ context.Context, evt event.UpdateEvent,
	q workqueue.TypedRateLimitingInterface[reconcile.Request],
) {
	//nolint:forcetypeassert
	newPolicySet := evt.ObjectNew.(*policiesv1beta1.PolicySet)
	//nolint:forcetypeassert
	oldPolicySet := evt.ObjectOld.(*policiesv1beta1.PolicySet)

	for _, policyName := range policySetUpdateDiff(oldPolicySet, newPolicySet) {
		log.V(2).Info("Found reconciliation request from a policyset", "policyName", policyName)

		q.Add(reconcile.Request{NamespacedName: types.NamespacedName{
			Name:      policyName,
			Namespace: newPolicySet.GetNamespace(),
		}})
	}
}

func policySetUpdateDiff(oldPolicySet, newPolicySet *policiesv1beta1.PolicySet) []string {
	oldPolicies := PolicyNamesInSet(oldPolicySet)
	newPolicies := PolicyNamesInSet(newPolicySet)
	oldExclusions := policySetExclusionsByPolicy(oldPolicySet)
	newExclusions := policySetExclusionsByPolicy(newPolicySet)

	seen := map[string]struct{}{}
	diff := []string{}

	add := func(policyName string) {
		if _, ok := seen[policyName]; ok {
			return
		}

		seen[policyName] = struct{}{}
		diff = append(diff, policyName)
	}

	for policyName := range newPolicies {
		if !IsPolicyListedInSet(oldPolicies, policyName) {
			add(policyName)
		}
	}

	for policyName := range oldPolicies {
		if !IsPolicyListedInSet(newPolicies, policyName) {
			add(policyName)
		}
	}

	for policyName, newExclusion := range newExclusions {
		oldExclusion, ok := oldExclusions[policyName]
		if !ok || !equality.Semantic.DeepEqual(newExclusion, oldExclusion) {
			add(policyName)
		}
	}

	for policyName := range oldExclusions {
		if _, ok := newExclusions[policyName]; !ok {
			add(policyName)
		}
	}

	return diff
}

func policySetExclusionsByPolicy(policySet *policiesv1beta1.PolicySet) map[string]policiesv1beta1.PolicySetExclusion {
	exclusions := make(map[string]policiesv1beta1.PolicySetExclusion, len(policySet.Spec.Exclusions))

	for _, exclusion := range policySet.Spec.Exclusions {
		exclusions[string(exclusion.PolicyName)] = exclusion
	}

	return exclusions
}

// Delete implements EventHandler
func (e *EnqueueRequestsFromPolicySet) Delete(_ context.Context, evt event.DeleteEvent,
	q workqueue.TypedRateLimitingInterface[reconcile.Request],
) {
	for _, policy := range mapPolicySetToRequests(evt.Object) {
		q.Add(policy)
	}
}

// Generic implements EventHandler
func (e *EnqueueRequestsFromPolicySet) Generic(_ context.Context, evt event.GenericEvent,
	q workqueue.TypedRateLimitingInterface[reconcile.Request],
) {
	for _, policy := range mapPolicySetToRequests(evt.Object) {
		q.Add(policy)
	}
}
