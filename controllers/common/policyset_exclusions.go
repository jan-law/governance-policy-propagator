// Copyright Contributors to the Open Cluster Management project

package common

import (
	"context"
	"fmt"
	"sort"
	"strings"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	policiesv1beta1 "open-cluster-management.io/governance-policy-propagator/api/v1beta1"
)

// PolicyNamesInSet returns policy names listed in spec.policies.
func PolicyNamesInSet(policySet *policiesv1beta1.PolicySet) map[string]struct{} {
	policiesInSet := make(map[string]struct{}, len(policySet.Spec.Policies))

	for _, policyName := range policySet.Spec.Policies {
		policiesInSet[string(policyName)] = struct{}{}
	}

	return policiesInSet
}

// IsPolicyListedInSet returns true when policyName is in the precomputed policiesInSet map.
func IsPolicyListedInSet(policiesInSet map[string]struct{}, policyName string) bool {
	_, ok := policiesInSet[policyName]

	return ok
}

// IsPolicyListedInPolicySet returns true when policyName is listed in spec.policies.
func IsPolicyListedInPolicySet(policySet *policiesv1beta1.PolicySet, policyName string) bool {
	return IsPolicyListedInSet(PolicyNamesInSet(policySet), policyName)
}

// GetPolicySet fetches a PolicySet, returning nil when it is not found.
func GetPolicySet(
	ctx context.Context, c client.Client, policySetName, namespace string,
) (*policiesv1beta1.PolicySet, error) {
	policySet := &policiesv1beta1.PolicySet{}
	setNN := types.NamespacedName{
		Name:      policySetName,
		Namespace: namespace,
	}

	if err := c.Get(ctx, setNN, policySet); err != nil {
		if k8serrors.IsNotFound(err) {
			return nil, nil
		}

		return nil, fmt.Errorf("failed to get PolicySet '%s': %w", policySetName, err)
	}

	return policySet, nil
}

// ExcludedClustersForPolicy returns managed clusters excluded for policyName in the PolicySet.
func ExcludedClustersForPolicy(policySet *policiesv1beta1.PolicySet, policyName string) map[string]struct{} {
	if len(policySet.Spec.Exclusions) == 0 || !IsPolicyListedInPolicySet(policySet, policyName) {
		return nil
	}

	return excludedClustersForListedPolicy(policySet, policyName)
}

// ExcludedClustersForListedPolicy returns excluded clusters when policyName is already known
// to be listed in spec.policies.
func ExcludedClustersForListedPolicy(policySet *policiesv1beta1.PolicySet, policyName string) map[string]struct{} {
	if len(policySet.Spec.Exclusions) == 0 {
		return nil
	}

	return excludedClustersForListedPolicy(policySet, policyName)
}

func excludedClustersForListedPolicy(policySet *policiesv1beta1.PolicySet, policyName string) map[string]struct{} {
	excluded := make(map[string]struct{})

	for _, exclusion := range policySet.Spec.Exclusions {
		if string(exclusion.PolicyName) != policyName {
			continue
		}

		for _, cluster := range exclusion.ClusterNames {
			excluded[string(cluster)] = struct{}{}
		}
	}

	if len(excluded) == 0 {
		return nil
	}

	return excluded
}

// IsPolicyExcludedForClusterInPolicySet returns true when the PolicySet excludes the policy from the cluster.
func IsPolicyExcludedForClusterInPolicySet(
	policySet *policiesv1beta1.PolicySet, policyName, clusterName string,
) bool {
	excluded := ExcludedClustersForPolicy(policySet, policyName)
	if len(excluded) == 0 {
		return false
	}

	_, ok := excluded[clusterName]

	return ok
}

// FilterExcludedPolicySetClusters removes clusters where the policy is excluded in the PolicySet.
func FilterExcludedPolicySetClusters(
	policySet *policiesv1beta1.PolicySet, policyName string, clusterDecisions []string,
) []string {
	included, _ := PartitionClusterDecisionsByPolicySetExclusion(policySet, policyName, clusterDecisions)

	return included
}

// FilterExcludedPolicySetClustersForListedPolicy removes excluded clusters when policyName is
// already known to be listed in spec.policies.
func FilterExcludedPolicySetClustersForListedPolicy(
	policySet *policiesv1beta1.PolicySet, policyName string, clusterDecisions []string,
) []string {
	included, _ := PartitionClusterDecisionsForListedPolicy(policySet, policyName, clusterDecisions)

	return included
}

// BuildPlacementPathExclusions returns placement-path exclusions for a policy on a PolicySet binding.
func BuildPlacementPathExclusions(
	policySet *policiesv1beta1.PolicySet, policyName string, clusterDecisions []string,
) []policiesv1.PolicyExclusion {
	_, pathExclusions := PartitionClusterDecisionsByPolicySetExclusion(policySet, policyName, clusterDecisions)
	if pathExclusions == nil {
		return []policiesv1.PolicyExclusion{}
	}

	return pathExclusions
}

// PolicySetExclusionSummary holds precomputed exclusion metadata for a PolicySet reconcile.
type PolicySetExclusionSummary struct {
	PoliciesInSet           map[string]struct{}
	ValidExclusions         []policiesv1beta1.PolicySetExclusion
	InvalidPolicyNames      []string
	StatusExclusions        []policiesv1beta1.PolicySetStatusExclusion
	ClusterExcludedMessages []string
}

// SummarizePolicySetExclusions classifies spec.exclusions in a single pass.
func SummarizePolicySetExclusions(policySet *policiesv1beta1.PolicySet) PolicySetExclusionSummary {
	summary := PolicySetExclusionSummary{
		PoliciesInSet: PolicyNamesInSet(policySet),
	}

	if len(policySet.Spec.Exclusions) == 0 {
		return summary
	}

	invalidSeen := make(map[string]struct{})

	for _, exclusion := range policySet.Spec.Exclusions {
		policyName := string(exclusion.PolicyName)
		if !IsPolicyListedInSet(summary.PoliciesInSet, policyName) {
			if _, ok := invalidSeen[policyName]; !ok {
				invalidSeen[policyName] = struct{}{}
				summary.InvalidPolicyNames = append(summary.InvalidPolicyNames, policyName)
			}

			continue
		}

		if len(exclusion.ClusterNames) == 0 {
			continue
		}

		summary.ValidExclusions = append(summary.ValidExclusions, exclusion)

		clusterNames := make([]string, 0, len(exclusion.ClusterNames))
		for _, cluster := range exclusion.ClusterNames {
			clusterNames = append(clusterNames, string(cluster))
		}

		summary.ClusterExcludedMessages = append(summary.ClusterExcludedMessages,
			fmt.Sprintf("%s (excluded from %s)", exclusion.PolicyName, strings.Join(clusterNames, ", ")))
	}

	summary.StatusExclusions = BuildPolicySetStatusExclusions(summary.ValidExclusions)

	return summary
}

// InvalidPolicySetExclusionPolicies returns policy names in spec.exclusions that are not in spec.policies.
func InvalidPolicySetExclusionPolicies(policySet *policiesv1beta1.PolicySet) []string {
	return SummarizePolicySetExclusions(policySet).InvalidPolicyNames
}

// FilterValidPolicySetExclusions returns exclusions whose policyName is listed in spec.policies.
func FilterValidPolicySetExclusions(policySet *policiesv1beta1.PolicySet) []policiesv1beta1.PolicySetExclusion {
	return SummarizePolicySetExclusions(policySet).ValidExclusions
}

// PartitionClusterDecisionsByPolicySetExclusion splits clusters into included decisions and path exclusions.
func PartitionClusterDecisionsByPolicySetExclusion(
	policySet *policiesv1beta1.PolicySet, policyName string, clusterDecisions []string,
) (included []string, pathExclusions []policiesv1.PolicyExclusion) {
	return partitionClusterDecisionsByExclusion(ExcludedClustersForPolicy(policySet, policyName), clusterDecisions)
}

// PartitionClusterDecisionsForListedPolicy splits cluster decisions when policyName is already
// known to be listed in spec.policies.
func PartitionClusterDecisionsForListedPolicy(
	policySet *policiesv1beta1.PolicySet, policyName string, clusterDecisions []string,
) (included []string, pathExclusions []policiesv1.PolicyExclusion) {
	return partitionClusterDecisionsByExclusion(
		ExcludedClustersForListedPolicy(policySet, policyName), clusterDecisions,
	)
}

func partitionClusterDecisionsByExclusion(
	excluded map[string]struct{}, clusterDecisions []string,
) (included []string, pathExclusions []policiesv1.PolicyExclusion) {
	if len(excluded) == 0 {
		return clusterDecisions, nil
	}

	included = make([]string, 0, len(clusterDecisions))

	for _, clusterName := range clusterDecisions {
		if _, ok := excluded[clusterName]; ok {
			pathExclusions = append(pathExclusions, policiesv1.PolicyExclusion{ClusterName: clusterName})
		} else {
			included = append(included, clusterName)
		}
	}

	return included, pathExclusions
}

// BuildPolicySetStatusExclusions returns de-duplicated status exclusions with one entry per policy.
func BuildPolicySetStatusExclusions(
	exclusions []policiesv1beta1.PolicySetExclusion,
) []policiesv1beta1.PolicySetStatusExclusion {
	if len(exclusions) == 0 {
		return nil
	}

	clustersByPolicy := map[string]map[string]struct{}{}
	policyOrder := []string{}

	for _, exclusion := range exclusions {
		policyName := string(exclusion.PolicyName)
		if len(exclusion.ClusterNames) == 0 {
			continue
		}

		if _, exists := clustersByPolicy[policyName]; !exists {
			clustersByPolicy[policyName] = map[string]struct{}{}
			policyOrder = append(policyOrder, policyName)
		}

		for _, cluster := range exclusion.ClusterNames {
			clustersByPolicy[policyName][string(cluster)] = struct{}{}
		}
	}

	statusExclusions := make([]policiesv1beta1.PolicySetStatusExclusion, 0, len(policyOrder))

	for _, policyName := range policyOrder {
		clusterNames := make([]string, 0, len(clustersByPolicy[policyName]))
		for clusterName := range clustersByPolicy[policyName] {
			clusterNames = append(clusterNames, clusterName)
		}

		sort.Strings(clusterNames)

		clusters := make([]policiesv1beta1.NonEmptyString, len(clusterNames))
		for i, clusterName := range clusterNames {
			clusters[i] = policiesv1beta1.NonEmptyString(clusterName)
		}

		statusExclusions = append(statusExclusions, policiesv1beta1.PolicySetStatusExclusion{
			PolicyName: policiesv1beta1.NonEmptyString(policyName),
			Clusters:   clusters,
		})
	}

	return statusExclusions
}
