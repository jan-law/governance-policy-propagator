// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"open-cluster-management.io/governance-policy-propagator/controllers/common"
	"open-cluster-management.io/governance-policy-propagator/test/utils"
)

var _ = FDescribe("Test policyset exclusions propagation", func() {
	const (
		path                   string = "../resources/case18_policyset_disabled/"
		case18Policy           string = "case18-test-policy"
		case18PolicySet        string = "case18-test-policyset"
		case18PolicySetPB      string = "case18-test-policyset-pb"
		case18PolicySetPLD     string = "case18-test-policyset-plm-decision"
		case18Yaml             string = path + "case18-test-policyset.yaml"
		case18SecondPolicyYaml string = path + "case18-second-policy.yaml"
	)

	Describe("PolicySet exclusions control per-cluster propagation", func() {
		It("should create the policyset resources", func(ctx SpecContext) {
			By("Creating " + case18Yaml)
			_, err := utils.KubectlWithOutput(ctx, "apply",
				"-f", case18Yaml,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
			Expect(err).ToNot(HaveOccurred())
			plcSet := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case18PolicySet, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plcSet).NotTo(BeNil())

			By("Ensuring placement decisions are available")
			pld := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementDecision, case18PolicySetPLD, testNamespace, true, defaultTimeoutSeconds,
			)
			pld.Object["status"] = utils.GeneratePldStatus(pld.GetName(), pld.GetNamespace(), "managed1", "managed2")
			_, err = clientHubDynamic.Resource(gvrPlacementDecision).Namespace(testNamespace).UpdateStatus(
				ctx, pld, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should not propagate to an excluded cluster before initial deployment", func(ctx SpecContext) {
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case18Policy, "managed1", true, defaultTimeoutSeconds,
			)
			Expect(plc).NotTo(BeNil())

			plc = utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case18Policy, "managed2", false, defaultTimeoutSeconds,
			)
			Expect(plc).To(BeNil())

			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case18Policy,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)

			plcSet := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case18PolicySet, testNamespace, true, defaultTimeoutSeconds,
			)
			status := plcSet.Object["status"].(map[string]interface{})
			Expect(status["statusMessage"]).To(ContainSubstring("Cluster excluded policies"))
			exclusions := status["exclusions"].([]interface{})
			Expect(exclusions).To(HaveLen(1))

			By("Checking root policy status for placement exclusions")
			Eventually(func(g Gomega) {
				rootPlc := utils.GetWithTimeout(
					clientHubDynamic, gvrPolicy, case18Policy, testNamespace, true, defaultTimeoutSeconds,
				)
				rootStatus, ok := rootPlc.Object["status"].(map[string]interface{})
				g.Expect(ok).To(BeTrue())

				placements, ok := rootStatus["placement"].([]interface{})
				g.Expect(ok).To(BeTrue())

				var policySetPlacement map[string]interface{}

				for _, placement := range placements {
					placementMap, ok := placement.(map[string]interface{})
					g.Expect(ok).To(BeTrue())

					if placementMap["policySet"] == case18PolicySet {
						policySetPlacement = placementMap
					}
				}

				g.Expect(policySetPlacement).ToNot(BeNil())
				g.Expect(policySetPlacement["placementBinding"]).To(Equal(case18PolicySetPB))

				pathExclusions, ok := policySetPlacement["exclusions"].([]interface{})
				g.Expect(ok).To(BeTrue())
				g.Expect(pathExclusions).To(HaveLen(1))

				pathExclusion, ok := pathExclusions[0].(map[string]interface{})
				g.Expect(ok).To(BeTrue())
				g.Expect(pathExclusion["clusterName"]).To(Equal("managed2"))

				clusterStatuses, ok := rootStatus["status"].([]interface{})
				g.Expect(ok).To(BeTrue())
				g.Expect(clusterStatuses).ToNot(BeEmpty())

				for _, clusterStatus := range clusterStatuses {
					clusterStatusMap, ok := clusterStatus.(map[string]interface{})
					g.Expect(ok).To(BeTrue())
					_, hasRemainingBindings := clusterStatusMap["remainingBindings"]
					g.Expect(hasRemainingBindings).To(BeFalse())
				}
			}, defaultTimeoutSeconds, 1).Should(Succeed())
		})

		It("should leave other policies in the policyset unaffected", func(ctx SpecContext) {
			By("Adding a second policy to the policyset without excluding it")
			_, err := utils.KubectlWithOutput(ctx, "apply",
				"-f", case18SecondPolicyYaml,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
			Expect(err).ToNot(HaveOccurred())

			secondPolicyName := testNamespace + ".case18-second-policy"
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, secondPolicyName, "managed1", true, defaultTimeoutSeconds,
			)
			Expect(plc).NotTo(BeNil())

			plc = utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, secondPolicyName, "managed2", true, defaultTimeoutSeconds,
			)
			Expect(plc).NotTo(BeNil())
		})

		It("should remove an already deployed policy from a newly excluded cluster", func(ctx SpecContext) {
			By("Removing exclusions so the first policy deploys to managed2")
			plcSet := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case18PolicySet, testNamespace, true, defaultTimeoutSeconds,
			)
			spec := plcSet.Object["spec"].(map[string]interface{})
			delete(spec, "exclusions")
			_, err := clientHubDynamic.Resource(gvrPolicySet).Namespace(testNamespace).Update(
				ctx, plcSet, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())

			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case18Policy, "managed2", true, defaultTimeoutSeconds,
			)
			Expect(plc).NotTo(BeNil())

			By("Excluding managed2 after the policy is deployed")
			plcSet = utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case18PolicySet, testNamespace, true, defaultTimeoutSeconds,
			)
			spec = plcSet.Object["spec"].(map[string]interface{})
			spec["exclusions"] = []map[string]interface{}{
				{
					"policyName":   case18Policy,
					"clusterNames": []string{"managed2"},
				},
			}
			_, err = clientHubDynamic.Resource(gvrPolicySet).Namespace(testNamespace).Update(
				ctx, plcSet, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())

			plc = utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case18Policy, "managed2", false, defaultTimeoutSeconds,
			)
			Expect(plc).To(BeNil())

			plc = utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case18Policy, "managed1", true, defaultTimeoutSeconds,
			)
			Expect(plc).NotTo(BeNil())
		})

		It("should re-propagate when exclusions is cleared", func(ctx SpecContext) {
			By("Clearing exclusions")
			plcSet := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case18PolicySet, testNamespace, true, defaultTimeoutSeconds,
			)
			spec := plcSet.Object["spec"].(map[string]interface{})
			delete(spec, "exclusions")
			_, err := clientHubDynamic.Resource(gvrPolicySet).Namespace(testNamespace).Update(
				ctx, plcSet, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())

			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case18Policy, "managed2", true, defaultTimeoutSeconds,
			)
			Expect(plc).NotTo(BeNil())

			By("Checking PolicySet and root policy status after exclusions are cleared")
			Eventually(func(g Gomega) {
				plcSet := utils.GetWithTimeout(
					clientHubDynamic, gvrPolicySet, case18PolicySet, testNamespace, true, defaultTimeoutSeconds,
				)
				status, ok := plcSet.Object["status"].(map[string]interface{})
				g.Expect(ok).To(BeTrue())

				statusMessage, ok := status["statusMessage"].(string)
				g.Expect(ok).To(BeTrue())
				g.Expect(statusMessage).NotTo(ContainSubstring("Cluster excluded policies"))

				_, hasPolicySetExclusions := status["exclusions"]
				g.Expect(hasPolicySetExclusions).To(BeFalse())

				rootPlc := utils.GetWithTimeout(
					clientHubDynamic, gvrPolicy, case18Policy, testNamespace, true, defaultTimeoutSeconds,
				)
				rootStatus, ok := rootPlc.Object["status"].(map[string]interface{})
				g.Expect(ok).To(BeTrue())

				placements, ok := rootStatus["placement"].([]interface{})
				g.Expect(ok).To(BeTrue())

				for _, placement := range placements {
					placementMap, ok := placement.(map[string]interface{})
					g.Expect(ok).To(BeTrue())

					if placementMap["policySet"] == case18PolicySet {
						_, hasExclusions := placementMap["exclusions"]
						g.Expect(hasExclusions).To(BeFalse())
					}
				}
			}, defaultTimeoutSeconds, 1).Should(Succeed())
		})

		It("should accept an optional exclusion reason", func(ctx SpecContext) {
			plcSet := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case18PolicySet, testNamespace, true, defaultTimeoutSeconds,
			)
			spec := plcSet.Object["spec"].(map[string]interface{})
			spec["exclusions"] = []map[string]interface{}{
				{
					"policyName":   case18Policy,
					"clusterNames": []string{"managed2"},
					"reason":       "incident mitigation",
				},
			}
			_, err := clientHubDynamic.Resource(gvrPolicySet).Namespace(testNamespace).Update(
				ctx, plcSet, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())

			plcSet = utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case18PolicySet, testNamespace, true, defaultTimeoutSeconds,
			)
			spec = plcSet.Object["spec"].(map[string]interface{})
			exclusions := spec["exclusions"].([]interface{})
			Expect(exclusions).To(HaveLen(1))

			exclusion := exclusions[0].(map[string]interface{})
			Expect(exclusion["reason"]).To(Equal("incident mitigation"))
		})

		It("should move the exclusion to a different cluster when patched", func(ctx SpecContext) {
			replicatedPolicyName := testNamespace + "." + case18Policy

			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, replicatedPolicyName, "managed2", false, defaultTimeoutSeconds,
			)
			Expect(plc).To(BeNil())

			plc = utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, replicatedPolicyName, "managed1", true, defaultTimeoutSeconds,
			)
			Expect(plc).NotTo(BeNil())

			By("Patching exclusion from managed2 to managed1")
			plcSet := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case18PolicySet, testNamespace, true, defaultTimeoutSeconds,
			)
			spec := plcSet.Object["spec"].(map[string]interface{})
			spec["exclusions"] = []map[string]interface{}{
				{
					"policyName":   case18Policy,
					"clusterNames": []string{"managed1"},
				},
			}
			_, err := clientHubDynamic.Resource(gvrPolicySet).Namespace(testNamespace).Update(
				ctx, plcSet, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())

			plc = utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, replicatedPolicyName, "managed1", false, defaultTimeoutSeconds,
			)
			Expect(plc).To(BeNil())

			plc = utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, replicatedPolicyName, "managed2", true, defaultTimeoutSeconds,
			)
			Expect(plc).NotTo(BeNil())

			Eventually(func(g Gomega) {
				updated := utils.GetWithTimeout(
					clientHubDynamic, gvrPolicySet, case18PolicySet, testNamespace, true, defaultTimeoutSeconds,
				)
				status, ok := updated.Object["status"].(map[string]interface{})
				g.Expect(ok).To(BeTrue())
				g.Expect(status["statusMessage"]).To(ContainSubstring("Cluster excluded policies"))

				exclusions, ok := status["exclusions"].([]interface{})
				g.Expect(ok).To(BeTrue())
				g.Expect(exclusions).To(HaveLen(1))

				statusExclusion, ok := exclusions[0].(map[string]interface{})
				g.Expect(ok).To(BeTrue())
				g.Expect(statusExclusion["policyName"]).To(Equal(case18Policy))

				clusters, ok := statusExclusion["clusters"].([]interface{})
				g.Expect(ok).To(BeTrue())
				g.Expect(clusters).To(ConsistOf("managed1"))

				rootPlc := utils.GetWithTimeout(
					clientHubDynamic, gvrPolicy, case18Policy, testNamespace, true, defaultTimeoutSeconds,
				)
				rootStatus, ok := rootPlc.Object["status"].(map[string]interface{})
				g.Expect(ok).To(BeTrue())

				placements, ok := rootStatus["placement"].([]interface{})
				g.Expect(ok).To(BeTrue())

				var policySetPlacement map[string]interface{}

				for _, placement := range placements {
					placementMap, ok := placement.(map[string]interface{})
					g.Expect(ok).To(BeTrue())

					if placementMap["policySet"] == case18PolicySet {
						policySetPlacement = placementMap
					}
				}

				g.Expect(policySetPlacement).ToNot(BeNil())

				pathExclusions, ok := policySetPlacement["exclusions"].([]interface{})
				g.Expect(ok).To(BeTrue())
				g.Expect(pathExclusions).To(HaveLen(1))

				pathExclusion, ok := pathExclusions[0].(map[string]interface{})
				g.Expect(ok).To(BeTrue())
				g.Expect(pathExclusion["clusterName"]).To(Equal("managed1"))
			}, defaultTimeoutSeconds, 1).Should(Succeed())
		})

		It("should report invalid exclusions for policies not in spec.policies", func(ctx SpecContext) {
			plcSet := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case18PolicySet, testNamespace, true, defaultTimeoutSeconds,
			)
			spec := plcSet.Object["spec"].(map[string]interface{})
			spec["exclusions"] = []map[string]interface{}{
				{
					"policyName":   "unknown-policy",
					"clusterNames": []string{"managed1"},
				},
			}
			_, err := clientHubDynamic.Resource(gvrPolicySet).Namespace(testNamespace).Update(
				ctx, plcSet, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())

			Eventually(func(g Gomega) {
				updated := utils.GetWithTimeout(
					clientHubDynamic, gvrPolicySet, case18PolicySet, testNamespace, true, defaultTimeoutSeconds,
				)
				status := updated.Object["status"].(map[string]interface{})
				statusMessage, ok := status["statusMessage"].(string)
				g.Expect(ok).To(BeTrue())
				g.Expect(statusMessage).To(ContainSubstring("Invalid exclusions: unknown-policy"))
			}, defaultTimeoutSeconds, 1).Should(Succeed())
		})

		It("should remove the policy when it is removed from the policyset", func(ctx SpecContext) {
			By("Patching policyset with only the second policy")
			plcSet := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case18PolicySet, testNamespace, true, defaultTimeoutSeconds,
			)
			spec := plcSet.Object["spec"].(map[string]interface{})
			spec["policies"] = []string{"case18-second-policy"}
			spec["exclusions"] = []map[string]interface{}{}
			_, err := clientHubDynamic.Resource(gvrPolicySet).Namespace(testNamespace).Update(
				ctx, plcSet, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())

			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case18Policy,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		})

		It("should clean up when the policyset is deleted with an active exclusion", func(ctx SpecContext) {
			replicatedPolicyName := testNamespace + "." + case18Policy

			By("Restoring policyset membership with an active exclusion")
			plcSet := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case18PolicySet, testNamespace, true, defaultTimeoutSeconds,
			)
			spec := plcSet.Object["spec"].(map[string]interface{})
			spec["policies"] = []string{case18Policy, "case18-second-policy"}
			spec["exclusions"] = []map[string]interface{}{
				{
					"policyName":   case18Policy,
					"clusterNames": []string{"managed2"},
				},
			}
			_, err := clientHubDynamic.Resource(gvrPolicySet).Namespace(testNamespace).Update(
				ctx, plcSet, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())

			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, replicatedPolicyName, "managed1", true, defaultTimeoutSeconds,
			)
			Expect(plc).NotTo(BeNil())

			plc = utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, replicatedPolicyName, "managed2", false, defaultTimeoutSeconds,
			)
			Expect(plc).To(BeNil())

			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + replicatedPolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)

			By("Deleting policyset while the exclusion is still active")
			_, err = utils.KubectlWithOutput(ctx, "delete", "policyset", case18PolicySet,
				"-n", testNamespace, "--kubeconfig="+kubeconfigHub)
			Expect(err).ToNot(HaveOccurred())

			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)

			plc = utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, replicatedPolicyName, "managed2", false, defaultTimeoutSeconds,
			)
			Expect(plc).To(BeNil())

			By("Deleting remaining resources")
			_, err = utils.KubectlWithOutput(ctx, "delete",
				"-f", case18Yaml,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub,
				"--ignore-not-found")
			Expect(err).ToNot(HaveOccurred())

			_, err = utils.KubectlWithOutput(ctx, "delete", "policy", "case18-second-policy",
				"-n", testNamespace, "--kubeconfig="+kubeconfigHub, "--ignore-not-found")
			Expect(err).ToNot(HaveOccurred())
		})
	})
})

var _ = Describe("Test policyset exclusions with multiple bindings", Ordered, func() {
	const (
		path                      string = "../resources/case18_policyset_disabled/"
		case18MultiPolicy         string = "case18-multi-policy"
		case18MultiPolicySet      string = "case18-multi-policyset"
		case18MultiBindingYaml    string = path + "case18-multi-binding.yaml"
		case18MultiDirectPB       string = "case18-multi-policy-direct-pb"
		case18MultiPolicySetPB    string = "case18-multi-policyset-pb"
		case18MultiDirectPLD      string = "case18-multi-policy-direct-plm-decision"
		case18MultiPolicySetPLD   string = "case18-multi-policyset-plm-decision"
	)

	AfterAll(func(ctx SpecContext) {
		_, _ = utils.KubectlWithOutput(ctx, "delete",
			"-f", case18MultiBindingYaml,
			"-n", testNamespace,
			"--kubeconfig="+kubeconfigHub,
			"--ignore-not-found")
		opt := metav1.ListOptions{
			LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case18MultiPolicy,
		}
		utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
	})

	It("should create the multi-binding resources", func(ctx SpecContext) {
		By("Creating " + case18MultiBindingYaml)
		_, err := utils.KubectlWithOutput(ctx, "apply",
			"-f", case18MultiBindingYaml,
			"-n", testNamespace,
			"--kubeconfig="+kubeconfigHub)
		Expect(err).ToNot(HaveOccurred())

		By("Ensuring placement decisions are available")
		directPLD := utils.GetWithTimeout(
			clientHubDynamic, gvrPlacementDecision, case18MultiDirectPLD, testNamespace, true, defaultTimeoutSeconds,
		)
		directPLD.Object["status"] = utils.GeneratePldStatus(
			directPLD.GetName(), directPLD.GetNamespace(), "managed2",
		)
		_, err = clientHubDynamic.Resource(gvrPlacementDecision).Namespace(testNamespace).UpdateStatus(
			ctx, directPLD, metav1.UpdateOptions{},
		)
		Expect(err).ToNot(HaveOccurred())

		policySetPLD := utils.GetWithTimeout(
			clientHubDynamic, gvrPlacementDecision, case18MultiPolicySetPLD, testNamespace, true, defaultTimeoutSeconds,
		)
		policySetPLD.Object["status"] = utils.GeneratePldStatus(
			policySetPLD.GetName(), policySetPLD.GetNamespace(), "managed1", "managed2",
		)
		_, err = clientHubDynamic.Resource(gvrPlacementDecision).Namespace(testNamespace).UpdateStatus(
			ctx, policySetPLD, metav1.UpdateOptions{},
		)
		Expect(err).ToNot(HaveOccurred())
	})

	It("should propagate via direct binding when policyset path excludes the cluster", func(ctx SpecContext) {
		By("Waiting for replicated policies on managed1 and managed2")
		plc := utils.GetWithTimeout(
			clientHubDynamic, gvrPolicy, testNamespace+"."+case18MultiPolicy, "managed1", true, defaultTimeoutSeconds,
		)
		Expect(plc).NotTo(BeNil())

		plc = utils.GetWithTimeout(
			clientHubDynamic, gvrPolicy, testNamespace+"."+case18MultiPolicy, "managed2", true, defaultTimeoutSeconds,
		)
		Expect(plc).NotTo(BeNil())

		opt := metav1.ListOptions{
			LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case18MultiPolicy,
		}
		utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)

		By("Checking root policy status for placement exclusions and remainingBindings")
		Eventually(func(g Gomega) {
			rootPlc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, case18MultiPolicy, testNamespace, true, defaultTimeoutSeconds,
			)
			status, ok := rootPlc.Object["status"].(map[string]interface{})
			g.Expect(ok).To(BeTrue())

			placements, ok := status["placement"].([]interface{})
			g.Expect(ok).To(BeTrue())

			var policySetPlacement map[string]interface{}

			for _, placement := range placements {
				placementMap, ok := placement.(map[string]interface{})
				g.Expect(ok).To(BeTrue())

				if placementMap["policySet"] == case18MultiPolicySet {
					policySetPlacement = placementMap
				}
			}

			g.Expect(policySetPlacement).ToNot(BeNil())
			g.Expect(policySetPlacement["placementBinding"]).To(Equal(case18MultiPolicySetPB))

			exclusions, ok := policySetPlacement["exclusions"].([]interface{})
			g.Expect(ok).To(BeTrue())
			g.Expect(exclusions).To(HaveLen(1))

			exclusion, ok := exclusions[0].(map[string]interface{})
			g.Expect(ok).To(BeTrue())
			g.Expect(exclusion["clusterName"]).To(Equal("managed2"))

			clusterStatuses, ok := status["status"].([]interface{})
			g.Expect(ok).To(BeTrue())

			var managed2Status map[string]interface{}

			for _, clusterStatus := range clusterStatuses {
				clusterStatusMap, ok := clusterStatus.(map[string]interface{})
				g.Expect(ok).To(BeTrue())

				if clusterStatusMap["clustername"] == "managed2" {
					managed2Status = clusterStatusMap
				}
			}

			g.Expect(managed2Status).ToNot(BeNil())

			remainingBindings, ok := managed2Status["remainingBindings"].([]interface{})
			g.Expect(ok).To(BeTrue())
			g.Expect(remainingBindings).To(HaveLen(1))

			remainingBinding, ok := remainingBindings[0].(map[string]interface{})
			g.Expect(ok).To(BeTrue())
			g.Expect(remainingBinding["placementBinding"]).To(Equal(case18MultiDirectPB))
		}, defaultTimeoutSeconds, 1).Should(Succeed())
	})

	It("should retain direct-bound placement when policyset is deleted with active exclusion", func(ctx SpecContext) {
		replicatedPolicyName := testNamespace + "." + case18MultiPolicy

		By("Deleting policyset while the exclusion remains active")
		_, err := utils.KubectlWithOutput(ctx, "delete", "policyset", case18MultiPolicySet,
			"-n", testNamespace, "--kubeconfig="+kubeconfigHub)
		Expect(err).ToNot(HaveOccurred())

		plc := utils.GetWithTimeout(
			clientHubDynamic, gvrPolicy, replicatedPolicyName, "managed2", true, defaultTimeoutSeconds,
		)
		Expect(plc).NotTo(BeNil())

		plc = utils.GetWithTimeout(
			clientHubDynamic, gvrPolicy, replicatedPolicyName, "managed1", false, defaultTimeoutSeconds,
		)
		Expect(plc).To(BeNil())

		opt := metav1.ListOptions{
			LabelSelector: common.RootPolicyLabel + "=" + replicatedPolicyName,
		}
		utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)

		Eventually(func(g Gomega) {
			rootPlc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, case18MultiPolicy, testNamespace, true, defaultTimeoutSeconds,
			)
			status, ok := rootPlc.Object["status"].(map[string]interface{})
			g.Expect(ok).To(BeTrue())

			placements, ok := status["placement"].([]interface{})
			g.Expect(ok).To(BeTrue())
			g.Expect(placements).To(HaveLen(1))
			g.Expect(placements[0].(map[string]interface{})["placementBinding"]).To(Equal(case18MultiDirectPB))
			_, hasPolicySet := placements[0].(map[string]interface{})["policySet"]
			g.Expect(hasPolicySet).To(BeFalse())

			clusterStatuses, ok := status["status"].([]interface{})
			g.Expect(ok).To(BeTrue())

			var managed2Status map[string]interface{}

			for _, clusterStatus := range clusterStatuses {
				clusterStatusMap, ok := clusterStatus.(map[string]interface{})
				g.Expect(ok).To(BeTrue())

				if clusterStatusMap["clustername"] == "managed2" {
					managed2Status = clusterStatusMap
				}
			}

			g.Expect(managed2Status).ToNot(BeNil())
			_, hasRemainingBindings := managed2Status["remainingBindings"]
			g.Expect(hasRemainingBindings).To(BeFalse())
		}, defaultTimeoutSeconds, 1).Should(Succeed())
	})
})
