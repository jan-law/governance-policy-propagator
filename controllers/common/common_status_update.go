// Copyright Contributors to the Open Cluster Management project

package common

import (
	"context"
	"fmt"
	"reflect"
	"sort"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	clusterv1beta1 "open-cluster-management.io/api/cluster/v1beta1"
	appsv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/placementrule/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	policiesv1beta1 "open-cluster-management.io/governance-policy-propagator/api/v1beta1"
)

// DecisionSet is a set of managed cluster names selected for policy propagation.
type DecisionSet map[string]bool

// RootStatusUpdate updates the root policy status with bound decisions, placements, and cluster status.
func RootStatusUpdate(ctx context.Context, c client.Client, rootPolicy *policiesv1.Policy) (DecisionSet, error) {
	placements, decisions, remainingBindings, err := collectRootPolicyPlacementState(ctx, c, rootPolicy)
	if err != nil {
		log.Info("Failed to get any placement decisions. Giving up on the request.")

		return nil, err
	}

	cpcs, cpcsErr := CalculatePerClusterStatus(ctx, c, rootPolicy, decisions)
	if cpcsErr != nil {
		// If there is a new replicated policy, then its lookup is expected to fail - it hasn't been created yet.
		log.Error(cpcsErr, "Failed to get at least one replicated policy, but that may be expected. Ignoring.")
	}

	for _, status := range cpcs {
		if bindings, ok := remainingBindings[status.ClusterName]; ok {
			status.RemainingBindings = bindings
		}
	}

	err = c.Get(ctx,
		types.NamespacedName{
			Namespace: rootPolicy.Namespace,
			Name:      rootPolicy.Name,
		}, rootPolicy)
	if err != nil {
		log.Error(err, "Failed to refresh the cached policy. Will use existing policy.")
	}

	complianceState := CalculateRootCompliance(cpcs)

	if reflect.DeepEqual(rootPolicy.Status.Status, cpcs) &&
		rootPolicy.Status.ComplianceState == complianceState &&
		reflect.DeepEqual(rootPolicy.Status.Placement, placements) {
		return decisions, nil
	}

	log.Info("Updating the root policy status", "RootPolicyName", rootPolicy.Name, "Namespace", rootPolicy.Namespace)
	rootPolicy.Status.Status = cpcs
	rootPolicy.Status.ComplianceState = complianceState
	rootPolicy.Status.Placement = placements

	err = c.Status().Update(ctx, rootPolicy)
	if err != nil {
		return nil, err
	}

	return decisions, nil
}

// GetPolicyPlacementDecisions retrieves the placement decisions for a input PlacementBinding when
// the policy is bound within it. It can return an error if the PlacementBinding is invalid, or if
// a required lookup fails.
func GetPolicyPlacementDecisions(ctx context.Context, c client.Client,
	instance *policiesv1.Policy, pb *policiesv1.PlacementBinding,
) (clusterDecisions []string, placements []*policiesv1.Placement, err error) {
	return getPolicyPlacementDecisions(ctx, c, instance, pb, nil, nil)
}

// GetClusterDecisions identifies all managed clusters which should have a replicated policy using the root policy.
// This returns unique decisions and placements that are NOT under Restricted subset.
// Also this function returns placements that are under restricted subset.
// But these placements include decisions which are under non-restricted subset.
// In other words, this function returns placements which include at least one decision under non-restricted subset.
func GetClusterDecisions(
	ctx context.Context,
	c client.Client,
	rootPolicy *policiesv1.Policy,
) (
	[]*policiesv1.Placement, DecisionSet, error,
) {
	log := log.WithValues("policyName", rootPolicy.GetName(), "policyNamespace", rootPolicy.GetNamespace())

	states, err := collectBindingPlacementStates(ctx, c, rootPolicy)
	if err != nil {
		log.Error(err, "Could not list the placement bindings")

		return nil, nil, err
	}

	placements, decisions := clusterDecisionsFromBindingStates(rootPolicy, states)

	return placements, decisions, nil
}

// ComputeRemainingBindings identifies placement bindings that still place a policy on a cluster when
// the policy is excluded on another PolicySet binding path.
func ComputeRemainingBindings(
	ctx context.Context,
	c client.Client,
	rootPolicy *policiesv1.Policy,
	decisions DecisionSet,
) (map[string][]policiesv1.RemainingBinding, error) {
	states, err := collectBindingPlacementStates(ctx, c, rootPolicy)
	if err != nil {
		return nil, err
	}

	return remainingBindingsFromBindingStates(rootPolicy, states, decisions), nil
}

// CalculatePerClusterStatus lists up all policies replicated from the input policy, and stores
// their compliance states in the result list. The result is sorted by cluster name. An error
// will be returned if lookup of a replicated policy fails, but all lookups will still be attempted.
func CalculatePerClusterStatus(
	ctx context.Context,
	c client.Client,
	rootPolicy *policiesv1.Policy,
	decisions DecisionSet,
) ([]*policiesv1.CompliancePerClusterStatus, error) {
	if rootPolicy.Spec.Disabled {
		return nil, nil
	}

	status := make([]*policiesv1.CompliancePerClusterStatus, 0, len(decisions))
	var lookupErr error // save until end, to attempt all lookups

	// Update the status based on the processed decisions
	for clusterName := range decisions {
		replicatedPolicy := &policiesv1.Policy{}
		key := types.NamespacedName{
			Namespace: clusterName, Name: rootPolicy.Namespace + "." + rootPolicy.Name,
		}

		err := c.Get(ctx, key, replicatedPolicy)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				status = append(status, &policiesv1.CompliancePerClusterStatus{
					ClusterName:      clusterName,
					ClusterNamespace: clusterName,
				})

				continue
			}

			lookupErr = err
		}

		status = append(status, &policiesv1.CompliancePerClusterStatus{
			ComplianceState:  replicatedPolicy.Status.ComplianceState,
			ClusterName:      clusterName,
			ClusterNamespace: clusterName,
		})
	}

	sort.Slice(status, func(i, j int) bool {
		return status[i].ClusterName < status[j].ClusterName
	})

	return status, lookupErr
}

// CalculateRootCompliance uses the input per-cluster statuses to determine what a root policy's
// ComplianceState should be. General precedence is: NonCompliant > Pending > Unknown > Compliant.
func CalculateRootCompliance(clusters []*policiesv1.CompliancePerClusterStatus) policiesv1.ComplianceState {
	if len(clusters) == 0 {
		// No clusters == no status
		return ""
	}

	unknownFound := false
	pendingFound := false

	for _, status := range clusters {
		switch status.ComplianceState {
		case policiesv1.NonCompliant:
			// NonCompliant has the highest priority, so we can skip checking the others
			return policiesv1.NonCompliant
		case policiesv1.Pending:
			pendingFound = true
		case policiesv1.Compliant:
			continue
		default:
			unknownFound = true
		}
	}

	if pendingFound {
		return policiesv1.Pending
	}

	if unknownFound {
		return ""
	}

	// Returns compliant if, and only if, *all* cluster statuses are Compliant
	return policiesv1.Compliant
}

type policyBindingSubjects struct {
	policySubjectFound bool
	policySetSubjects  map[string]*policiesv1beta1.PolicySet
	placements         []*policiesv1.Placement
}

type bindingPlacementState struct {
	pb                *policiesv1.PlacementBinding
	rawDecisions      []string
	filteredDecisions []string
	placements        []*policiesv1.Placement
	trackRemaining    bool
}

func resolvePolicyBindingSubjects(
	ctx context.Context,
	c client.Client,
	instance *policiesv1.Policy,
	pb *policiesv1.PlacementBinding,
) (policyBindingSubjects, error) {
	subjects := policyBindingSubjects{
		policySetSubjects: make(map[string]*policiesv1beta1.PolicySet),
	}

	for _, subject := range pb.Subjects {
		if subject.APIGroup != policiesv1.SchemeGroupVersion.Group {
			continue
		}

		switch subject.Kind {
		case policiesv1.Kind:
			if !subjects.policySubjectFound && subject.Name == instance.GetName() {
				subjects.policySubjectFound = true

				subjects.placements = append(subjects.placements, &policiesv1.Placement{
					PlacementBinding: pb.GetName(),
				})
			}
		case policiesv1.PolicySetKind:
			if _, exists := subjects.policySetSubjects[subject.Name]; exists {
				continue
			}

			policySet, getErr := GetPolicySet(ctx, c, subject.Name, pb.GetNamespace())
			if getErr != nil {
				return policyBindingSubjects{}, getErr
			}

			if policySet == nil || !IsPolicyListedInPolicySet(policySet, instance.GetName()) {
				continue
			}

			subjects.policySetSubjects[subject.Name] = policySet

			subjects.placements = append(subjects.placements, &policiesv1.Placement{
				PlacementBinding: pb.GetName(),
				PolicySet:        subject.Name,
			})
		}
	}

	return subjects, nil
}

func getPolicyPlacementDecisions(
	ctx context.Context,
	c client.Client,
	instance *policiesv1.Policy,
	pb *policiesv1.PlacementBinding,
	rawDecisions []string,
	resolvedSubjects *policyBindingSubjects,
) (clusterDecisions []string, placements []*policiesv1.Placement, err error) {
	subjects := resolvedSubjects
	if subjects == nil {
		var resolved policyBindingSubjects

		resolved, err = resolvePolicyBindingSubjects(ctx, c, instance, pb)
		if err != nil {
			return nil, nil, err
		}

		subjects = &resolved
	}

	placements = subjects.placements

	if len(placements) == 0 {
		// None of the subjects in the PlacementBinding were relevant to this Policy.
		return nil, nil, nil
	}

	// If the PlacementRef is invalid, log and return. (This is not recoverable.)
	if !HasValidPlacementRef(pb) {
		log.Info(fmt.Sprintf("Placement binding %s/%s placementRef is not valid. Ignoring.", pb.Namespace, pb.Name))

		return nil, nil, nil
	}

	// If the placementRef exists, then it needs to be added to the placement item
	refNN := types.NamespacedName{
		Namespace: pb.GetNamespace(),
		Name:      pb.PlacementRef.Name,
	}

	switch pb.PlacementRef.Kind {
	case "PlacementRule":
		plr := &appsv1.PlacementRule{}
		if err := c.Get(ctx, refNN, plr); err != nil && !k8serrors.IsNotFound(err) {
			return nil, nil, fmt.Errorf("failed to check for PlacementRule '%v': %w", pb.PlacementRef.Name, err)
		}

		for i := range placements {
			placements[i].PlacementRule = plr.Name // will be empty if the PlacementRule was not found
		}
	case "Placement":
		pl := &clusterv1beta1.Placement{}
		if err := c.Get(ctx, refNN, pl); err != nil && !k8serrors.IsNotFound(err) {
			return nil, nil, fmt.Errorf("failed to check for Placement '%v': %w", pb.PlacementRef.Name, err)
		}

		for i := range placements {
			placements[i].Placement = pl.Name // will be empty if the Placement was not found
		}
	}

	// If there are no placements, then the PlacementBinding is not for this Policy.
	if len(placements) == 0 {
		return nil, nil, nil
	}

	// If the policy is disabled, don't return any decisions, so that the policy isn't put on any clusters
	if instance.Spec.Disabled {
		return nil, placements, nil
	}

	if rawDecisions != nil {
		clusterDecisions = append([]string(nil), rawDecisions...)
	} else {
		clusterDecisions, err = GetDecisions(ctx, c, pb)
		if err != nil {
			return nil, placements, err
		}
	}

	return applyPolicySetExclusionsToDecisions(
		instance, subjects.policySubjectFound, subjects.policySetSubjects, placements, clusterDecisions,
	)
}

func applyPolicySetExclusionsToDecisions(
	instance *policiesv1.Policy,
	policySubjectFound bool,
	policySetSubjects map[string]*policiesv1beta1.PolicySet,
	placements []*policiesv1.Placement,
	clusterDecisions []string,
) ([]string, []*policiesv1.Placement, error) {
	if policySubjectFound || len(policySetSubjects) == 0 {
		return clusterDecisions, placements, nil
	}

	pathDecisionSource := append([]string(nil), clusterDecisions...)

	for policySetName, policySet := range policySetSubjects {
		_, pathExclusions := PartitionClusterDecisionsForListedPolicy(
			policySet, instance.GetName(), pathDecisionSource,
		)

		for i := range placements {
			if placements[i].PolicySet == policySetName {
				placements[i].Exclusions = pathExclusions
			}
		}

		clusterDecisions, _ = PartitionClusterDecisionsForListedPolicy(
			policySet, instance.GetName(), clusterDecisions,
		)
	}

	return clusterDecisions, placements, nil
}

func resolveBindingPlacementState(
	ctx context.Context,
	c client.Client,
	rootPolicy *policiesv1.Policy,
	pb *policiesv1.PlacementBinding,
) (bindingPlacementState, error) {
	subjects, err := resolvePolicyBindingSubjects(ctx, c, rootPolicy, pb)
	if err != nil {
		return bindingPlacementState{}, err
	}

	state := bindingPlacementState{
		pb:             pb,
		trackRemaining: len(subjects.placements) > 0,
	}

	var rawDecisions []string

	if state.trackRemaining {
		rawDecisions, err = GetDecisions(ctx, c, pb)
		if err != nil {
			return bindingPlacementState{}, err
		}

		state.rawDecisions = rawDecisions
	}

	filteredDecisions, placements, err := getPolicyPlacementDecisions(ctx, c, rootPolicy, pb, rawDecisions, &subjects)
	if err != nil {
		return bindingPlacementState{}, err
	}

	state.filteredDecisions = filteredDecisions
	state.placements = placements

	return state, nil
}

func collectBindingPlacementStates(
	ctx context.Context, c client.Client, rootPolicy *policiesv1.Policy,
) ([]bindingPlacementState, error) {
	pbList := &policiesv1.PlacementBindingList{}

	err := c.List(ctx, pbList, &client.ListOptions{Namespace: rootPolicy.GetNamespace()})
	if err != nil {
		return nil, err
	}

	states := make([]bindingPlacementState, 0, len(pbList.Items))

	for i := range pbList.Items {
		state, resolveErr := resolveBindingPlacementState(ctx, c, rootPolicy, &pbList.Items[i])
		if resolveErr != nil {
			return nil, resolveErr
		}

		states = append(states, state)
	}

	return states, nil
}

func collectRootPolicyPlacementState(
	ctx context.Context, c client.Client, rootPolicy *policiesv1.Policy,
) ([]*policiesv1.Placement, DecisionSet, map[string][]policiesv1.RemainingBinding, error) {
	states, err := collectBindingPlacementStates(ctx, c, rootPolicy)
	if err != nil {
		return nil, nil, nil, err
	}

	placements, decisions := clusterDecisionsFromBindingStates(rootPolicy, states)
	remainingBindings := remainingBindingsFromBindingStates(rootPolicy, states, decisions)

	return placements, decisions, remainingBindings, nil
}

func clusterDecisionsFromBindingStates(
	rootPolicy *policiesv1.Policy, states []bindingPlacementState,
) ([]*policiesv1.Placement, DecisionSet) {
	log := log.WithValues("policyName", rootPolicy.GetName(), "policyNamespace", rootPolicy.GetNamespace())
	decisions := make(map[string]bool)
	placements := []*policiesv1.Placement{}

	for _, state := range states {
		if state.pb.SubFilter == policiesv1.Restricted {
			continue
		}

		if len(state.filteredDecisions) == 0 {
			log.V(1).Info("No placement decisions to process for this policy from this non-restricted binding",
				"policyName", rootPolicy.GetName(), "bindingName", state.pb.GetName())
		}

		for _, clusterName := range state.filteredDecisions {
			decisions[clusterName] = true
		}

		placements = append(placements, state.placements...)
	}

	for _, state := range states {
		if state.pb.SubFilter != policiesv1.Restricted {
			continue
		}

		foundInDecisions := false

		if len(state.filteredDecisions) == 0 {
			log.V(1).Info("No placement decisions to process for this policy from this restricted binding",
				"policyName", rootPolicy.GetName(), "bindingName", state.pb.GetName())
		}

		for _, clusterName := range state.filteredDecisions {
			if _, ok := decisions[clusterName]; ok {
				foundInDecisions = true
			}

			decisions[clusterName] = true
		}

		if foundInDecisions {
			placements = append(placements, state.placements...)
		}
	}

	log.V(2).Info("Sorting placements", "RootPolicyName", rootPolicy.Name, "Namespace", rootPolicy.Namespace)
	sortPlacements(placements)

	return placements, decisions
}

func remainingBindingsFromBindingStates(
	rootPolicy *policiesv1.Policy, states []bindingPlacementState, decisions DecisionSet,
) map[string][]policiesv1.RemainingBinding {
	if rootPolicy.Spec.Disabled || len(decisions) == 0 {
		return nil
	}

	remainingBindings := map[string][]policiesv1.RemainingBinding{}

	for clusterName := range decisions {
		activeBindings := []policiesv1.RemainingBinding{}
		excludedOnPath := false

		for _, state := range states {
			if !state.trackRemaining {
				continue
			}

			filtered := false
			for _, name := range state.filteredDecisions {
				if name == clusterName {
					filtered = true

					break
				}
			}

			if filtered {
				activeBindings = append(activeBindings, policiesv1.RemainingBinding{
					PlacementBinding: state.pb.GetName(),
				})
			}

			for _, name := range state.rawDecisions {
				if name == clusterName && !filtered {
					excludedOnPath = true

					break
				}
			}
		}

		if excludedOnPath && len(activeBindings) > 0 {
			sort.Slice(activeBindings, func(i, j int) bool {
				return activeBindings[i].PlacementBinding < activeBindings[j].PlacementBinding
			})
			remainingBindings[clusterName] = activeBindings
		}
	}

	return remainingBindings
}

func sortPlacements(placements []*policiesv1.Placement) {
	sort.SliceStable(placements, func(i, j int) bool {
		pi := placements[i].PlacementBinding + " " + placements[i].Placement + " " +
			placements[i].PlacementRule + " " + placements[i].PolicySet
		pj := placements[j].PlacementBinding + " " + placements[j].Placement + " " +
			placements[j].PlacementRule + " " + placements[j].PolicySet

		return pi < pj
	})
}
