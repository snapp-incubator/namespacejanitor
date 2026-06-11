package controller

import (
	"context"
	"path/filepath"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	snappcloudv1alpha1 "github.com/snapp-incubator/namespacejanitor/api/v1alpha1"
)

// testConfig loads the test LifecycleConfig from config/test/config.yaml.
func testConfig() LifecycleConfig {
	_, thisFile, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(thisFile), "..", "..", "config", "test", "config.yaml")
	cfg, err := LoadConfig(path)
	if err != nil {
		panic("failed to load test config: " + err.Error())
	}
	return cfg.Lifecycle
}

var _ = Describe("NamespaceJanitor Controller", func() {

	Context("when a relevant Namespace is created", func() {
		var (
			ctx           context.Context
			testNamespace *corev1.Namespace
			namespaceName string
		)

		BeforeEach(func() {
			ctx = context.Background()
			namespaceName = "test-ns-for-cr-creation"
			testNamespace = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: namespaceName,
					Labels: map[string]string{
						TeamLabelKey: TeamUnknown,
					},
				},
			}
			By("Creating a new Namespace with 'team=unknown' label")
			Expect(k8sClient.Create(ctx, testNamespace)).To(Succeed())
		})

		AfterEach(func() {
			By("Cleaning up the test namespace")
			deletePolicy := metav1.DeletePropagationForeground
			_ = k8sClient.Delete(ctx, testNamespace, &client.DeleteOptions{
				PropagationPolicy: &deletePolicy,
			})
		})

		It("should create a default NamespaceJanitor CR inside it", func() {
			controllerReconciler := &NamespaceJanitorReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
				Config: testConfig(),
			}
			req := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      janitorCRName(namespaceName),
					Namespace: namespaceName,
				},
			}

			By("Running the reconciliation loop")
			result, err := controllerReconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(time.Second))

			By("Verifying that the default NamespaceJanitor CR was created")
			createdCR := &snappcloudv1alpha1.NamespaceJanitor{}
			crNamespacedName := types.NamespacedName{Name: janitorCRName(namespaceName), Namespace: namespaceName}
			Eventually(func() error {
				return k8sClient.Get(ctx, crNamespacedName, createdCR)
			}, time.Second*5, time.Millisecond*250).Should(Succeed())

			By("Verifying the created CR has an empty spec")
			Expect(createdCR.Spec.AdditionalRecipients).To(BeEmpty())
		})
	})

	Context("when managing the lifecycle of an 'unknown' Namespace", func() {
		var (
			ctx                  context.Context
			controllerReconciler *NamespaceJanitorReconciler
		)

		BeforeEach(func() {
			ctx = context.Background()
			controllerReconciler = &NamespaceJanitorReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
				Config: testConfig(),
			}
		})

		It("should apply the yellow flag when the age exceeds YellowThreshold", func() {
			namespaceName := "test-ns-yellow"
			nsKey := types.NamespacedName{Name: namespaceName}
			testNamespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:   namespaceName,
					Labels: map[string]string{TeamLabelKey: TeamUnknown},
				},
			}
			Expect(k8sClient.Create(ctx, testNamespace)).To(Succeed())
			Expect(k8sClient.Create(ctx, &snappcloudv1alpha1.NamespaceJanitor{
				ObjectMeta: metav1.ObjectMeta{Name: janitorCRName(namespaceName), Namespace: namespaceName},
			})).To(Succeed())

			By("Waiting for the YellowThreshold to be exceeded")
			time.Sleep(testConfig().YellowThreshold.Duration + time.Millisecond*200)

			By("Reconciling to apply the yellow flag")
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: janitorCRName(namespaceName), Namespace: namespaceName}})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying the yellow flag is present")
			Eventually(func(g Gomega) {
				currentNS := &corev1.Namespace{}
				g.Expect(k8sClient.Get(ctx, nsKey, currentNS)).To(Succeed())
				g.Expect(currentNS.Labels).To(HaveKeyWithValue(FlagLabelKey, FlagYellow))
			}, time.Second*5, time.Millisecond*250).Should(Succeed())

			// Cleanup
			Expect(k8sClient.Delete(ctx, testNamespace)).To(Succeed())
		})

		It("should apply the red flag when the age exceeds RedThreshold", func() {
			namespaceName := "test-ns-red"
			nsKey := types.NamespacedName{Name: namespaceName}
			testNamespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:   namespaceName,
					Labels: map[string]string{TeamLabelKey: TeamUnknown, FlagLabelKey: FlagYellow},
				},
			}
			Expect(k8sClient.Create(ctx, testNamespace)).To(Succeed())
			Expect(k8sClient.Create(ctx, &snappcloudv1alpha1.NamespaceJanitor{
				ObjectMeta: metav1.ObjectMeta{Name: janitorCRName(namespaceName), Namespace: namespaceName},
			})).To(Succeed())

			By("Waiting for the RedThreshold to be exceeded")
			time.Sleep(testConfig().RedThreshold.Duration + time.Millisecond*200)

			By("Reconciling to apply the red flag")
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: janitorCRName(namespaceName), Namespace: namespaceName}})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying the red flag is present")
			Eventually(func(g Gomega) {
				currentNS := &corev1.Namespace{}
				g.Expect(k8sClient.Get(ctx, nsKey, currentNS)).To(Succeed())
				g.Expect(currentNS.Labels).To(HaveKeyWithValue(FlagLabelKey, FlagRed))
			}, time.Second*5, time.Millisecond*250).Should(Succeed())

			// Cleanup
			Expect(k8sClient.Delete(ctx, testNamespace)).To(Succeed())
		})

		It("should send final warning when age exceeds FinalWarningThreshold", func() {
			namespaceName := "test-ns-finalwarn"
			nsKey := types.NamespacedName{Name: namespaceName}
			testNamespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:   namespaceName,
					Labels: map[string]string{TeamLabelKey: TeamUnknown, FlagLabelKey: FlagRed},
				},
			}
			Expect(k8sClient.Create(ctx, testNamespace)).To(Succeed())
			Expect(k8sClient.Create(ctx, &snappcloudv1alpha1.NamespaceJanitor{
				ObjectMeta: metav1.ObjectMeta{Name: janitorCRName(namespaceName), Namespace: namespaceName},
			})).To(Succeed())

			By("Waiting for the FinalWarningThreshold to be exceeded")
			time.Sleep(testConfig().FinalWarningThreshold.Duration + time.Millisecond*200)

			By("Reconciling to trigger final warning")
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: janitorCRName(namespaceName), Namespace: namespaceName}})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying the final-warning label is present")
			Eventually(func(g Gomega) {
				currentNS := &corev1.Namespace{}
				g.Expect(k8sClient.Get(ctx, nsKey, currentNS)).To(Succeed())
				g.Expect(currentNS.Labels).To(HaveKeyWithValue(FinalWarningLabelKey, "sent"))
				// Red flag should still be there
				g.Expect(currentNS.Labels).To(HaveKeyWithValue(FlagLabelKey, FlagRed))
			}, time.Second*5, time.Millisecond*250).Should(Succeed())

			// Cleanup
			Expect(k8sClient.Delete(ctx, testNamespace)).To(Succeed())
		})

		It("should delete the namespace when the age exceeds DeleteThreshold after final warning", func() {
			namespaceName := "test-ns-delete"
			nsKey := types.NamespacedName{Name: namespaceName}
			testNamespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: namespaceName,
					Labels: map[string]string{
						TeamLabelKey:         TeamUnknown,
						FlagLabelKey:         FlagRed,
						FinalWarningLabelKey: "sent",
					},
				},
			}
			Expect(k8sClient.Create(ctx, testNamespace)).To(Succeed())
			Expect(k8sClient.Create(ctx, &snappcloudv1alpha1.NamespaceJanitor{
				ObjectMeta: metav1.ObjectMeta{Name: janitorCRName(namespaceName), Namespace: namespaceName},
			})).To(Succeed())

			By("Waiting for the DeleteThreshold to be exceeded")
			time.Sleep(testConfig().DeleteThreshold.Duration + time.Millisecond*200)

			By("Reconciling to delete the namespace")
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: janitorCRName(namespaceName), Namespace: namespaceName}})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying the namespace is Terminating")
			Eventually(func(g Gomega) {
				terminatingNS := &corev1.Namespace{}
				g.Expect(k8sClient.Get(ctx, nsKey, terminatingNS)).To(Succeed())
				g.Expect(terminatingNS.DeletionTimestamp).NotTo(BeNil())
			}, time.Second*10, time.Millisecond*250).Should(Succeed())
		})
	})

	Context("when an 'unknown' namespace is claimed by a team", func() {
		var (
			ctx           context.Context
			testNamespace *corev1.Namespace
			janitorCR     *snappcloudv1alpha1.NamespaceJanitor
			namespaceName string
		)

		BeforeEach(func() {
			ctx = context.Background()
			namespaceName = "test-ns-cleanup"
			testNamespace = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: namespaceName,
					Labels: map[string]string{
						TeamLabelKey: TeamUnknown,
						FlagLabelKey: FlagYellow,
					},
				},
			}
			janitorCR = &snappcloudv1alpha1.NamespaceJanitor{
				ObjectMeta: metav1.ObjectMeta{
					Name:      janitorCRName(namespaceName),
					Namespace: namespaceName,
				},
			}
			Expect(k8sClient.Create(ctx, testNamespace)).To(Succeed())
			Expect(k8sClient.Create(ctx, janitorCR)).To(Succeed())
		})

		AfterEach(func() {
			_ = k8sClient.Delete(ctx, testNamespace)
		})

		It("should remove the flag label and update the CR status", func() {
			controllerReconciler := &NamespaceJanitorReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
				Config: testConfig(),
			}
			req := reconcile.Request{
				NamespacedName: types.NamespacedName{Name: janitorCRName(namespaceName), Namespace: namespaceName},
			}
			nsKey := types.NamespacedName{Name: namespaceName}
			crKey := req.NamespacedName

			By("Simulating the namespace being claimed by a team")
			currentNS := &corev1.Namespace{}
			Expect(k8sClient.Get(ctx, nsKey, currentNS)).To(Succeed())
			currentNS.Labels[TeamLabelKey] = "payments-team"
			Expect(k8sClient.Update(ctx, currentNS)).To(Succeed())

			By("Reconciling to trigger the cleanup logic")
			_, err := controllerReconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			By("Verifying the flag label has been removed")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, nsKey, currentNS)).To(Succeed())
				g.Expect(currentNS.Labels).ShouldNot(HaveKey(FlagLabelKey))
			}, time.Second*5, time.Millisecond*250).Should(Succeed())

			By("Verifying the Janitor CR status is updated")
			updatedCR := &snappcloudv1alpha1.NamespaceJanitor{}
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, crKey, updatedCR)).To(Succeed())
				g.Expect(updatedCR.Status.Conditions).NotTo(BeEmpty())
				g.Expect(updatedCR.Status.Conditions[0].Reason).To(Equal("TeamClaimed"))
			}, time.Second*5, time.Millisecond*250).Should(Succeed())
		})
	})

	Context("E2E: notifications with requester tracking", func() {
		var (
			ctx                  context.Context
			mockNotifier         *MockNotifier
			controllerReconciler *NamespaceJanitorReconciler
		)

		BeforeEach(func() {
			ctx = context.Background()
			mockNotifier = &MockNotifier{}
			controllerReconciler = &NamespaceJanitorReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Notifier: mockNotifier,
				Config:   testConfig(),
			}
		})

		It("should send creation notification on first reconcile", func() {
			namespaceName := "test-e2e-creation"
			nsKey := types.NamespacedName{Name: namespaceName}
			testNamespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: namespaceName,
					Labels: map[string]string{
						TeamLabelKey: TeamUnknown,
					},
					Annotations: map[string]string{
						RequesterAnnotationKey: "mohammadreza.saberi",
					},
				},
			}
			Expect(k8sClient.Create(ctx, testNamespace)).To(Succeed())
			Expect(k8sClient.Create(ctx, &snappcloudv1alpha1.NamespaceJanitor{
				ObjectMeta: metav1.ObjectMeta{Name: janitorCRName(namespaceName), Namespace: namespaceName},
			})).To(Succeed())

			By("Reconciling immediately (namespace just created)")
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: janitorCRName(namespaceName), Namespace: namespaceName},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying creation notification was sent")
			payloads := mockNotifier.GetPayloads()
			Expect(payloads).To(HaveLen(1))
			Expect(payloads[0].ActionTaken).To(Equal("NamespaceCreated"))
			Expect(payloads[0].Requester).To(Equal("mohammadreza.saberi"))

			By("Verifying creation-notified label was applied")
			Eventually(func(g Gomega) {
				currentNS := &corev1.Namespace{}
				g.Expect(k8sClient.Get(ctx, nsKey, currentNS)).To(Succeed())
				g.Expect(currentNS.Labels).To(HaveKeyWithValue(CreationNotifiedLabelKey, "true"))
			}, time.Second*5, time.Millisecond*250).Should(Succeed())

			By("Reconciling again — creation notification must NOT re-send (idempotency)")
			mockNotifier.Reset()
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: janitorCRName(namespaceName), Namespace: namespaceName},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(mockNotifier.GetPayloads()).To(BeEmpty())

			// Cleanup
			Expect(k8sClient.Delete(ctx, testNamespace)).To(Succeed())
		})

		It("should send yellow flag notification with requester", func() {
			namespaceName := "test-e2e-yellow"
			nsKey := types.NamespacedName{Name: namespaceName}
			testNamespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: namespaceName,
					Labels: map[string]string{
						TeamLabelKey: TeamUnknown,
					},
					Annotations: map[string]string{
						RequesterAnnotationKey: "mohammadreza.saberi",
					},
				},
			}
			Expect(k8sClient.Create(ctx, testNamespace)).To(Succeed())
			Expect(k8sClient.Create(ctx, &snappcloudv1alpha1.NamespaceJanitor{
				ObjectMeta: metav1.ObjectMeta{Name: janitorCRName(namespaceName), Namespace: namespaceName},
			})).To(Succeed())

			By("Waiting for the YellowThreshold to be exceeded")
			time.Sleep(testConfig().YellowThreshold.Duration + time.Millisecond*200)

			By("Reconciling to apply the yellow flag")
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: janitorCRName(namespaceName), Namespace: namespaceName},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying the yellow flag is applied")
			Eventually(func(g Gomega) {
				currentNS := &corev1.Namespace{}
				g.Expect(k8sClient.Get(ctx, nsKey, currentNS)).To(Succeed())
				g.Expect(currentNS.Labels).To(HaveKeyWithValue(FlagLabelKey, FlagYellow))
			}, time.Second*5, time.Millisecond*250).Should(Succeed())

			By("Verifying notifications: creation + yellow flag")
			payloads := mockNotifier.GetPayloads()
			Expect(len(payloads)).To(BeNumerically(">=", 2))
			// Last notification should be the yellow flag
			last := payloads[len(payloads)-1]
			Expect(last.ActionTaken).To(Equal("AppliedyellowFlag"))
			Expect(last.NamespaceName).To(Equal(namespaceName))
			Expect(last.Requester).To(Equal("mohammadreza.saberi"))

			// Cleanup
			Expect(k8sClient.Delete(ctx, testNamespace)).To(Succeed())
		})

		It("should send red flag notification with requester", func() {
			namespaceName := "test-e2e-red"
			nsKey := types.NamespacedName{Name: namespaceName}
			testNamespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: namespaceName,
					Labels: map[string]string{
						TeamLabelKey: TeamUnknown,
						FlagLabelKey: FlagYellow,
					},
					Annotations: map[string]string{
						RequesterAnnotationKey: "mohammadreza.saberi",
					},
				},
			}
			Expect(k8sClient.Create(ctx, testNamespace)).To(Succeed())
			Expect(k8sClient.Create(ctx, &snappcloudv1alpha1.NamespaceJanitor{
				ObjectMeta: metav1.ObjectMeta{Name: janitorCRName(namespaceName), Namespace: namespaceName},
			})).To(Succeed())

			By("Waiting for the RedThreshold to be exceeded")
			time.Sleep(testConfig().RedThreshold.Duration + time.Millisecond*200)

			By("Reconciling to apply the red flag")
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: janitorCRName(namespaceName), Namespace: namespaceName},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying the red flag is applied")
			Eventually(func(g Gomega) {
				currentNS := &corev1.Namespace{}
				g.Expect(k8sClient.Get(ctx, nsKey, currentNS)).To(Succeed())
				g.Expect(currentNS.Labels).To(HaveKeyWithValue(FlagLabelKey, FlagRed))
			}, time.Second*5, time.Millisecond*250).Should(Succeed())

			By("Verifying notification was sent with correct payload")
			payloads := mockNotifier.GetPayloads()
			Expect(payloads).To(ContainElement(SatisfyAll(
				HaveField("ActionTaken", "AppliedredFlag"),
				HaveField("Requester", "mohammadreza.saberi"),
			)))

			// Cleanup
			Expect(k8sClient.Delete(ctx, testNamespace)).To(Succeed())
		})

		It("should send final warning then delete the namespace", func() {
			namespaceName := "test-e2e-finalwarn"
			nsKey := types.NamespacedName{Name: namespaceName}
			testNamespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: namespaceName,
					Labels: map[string]string{
						TeamLabelKey: TeamUnknown,
						FlagLabelKey: FlagRed,
					},
					Annotations: map[string]string{
						RequesterAnnotationKey: "mohammadreza.saberi",
					},
				},
			}
			Expect(k8sClient.Create(ctx, testNamespace)).To(Succeed())
			Expect(k8sClient.Create(ctx, &snappcloudv1alpha1.NamespaceJanitor{
				ObjectMeta: metav1.ObjectMeta{Name: janitorCRName(namespaceName), Namespace: namespaceName},
			})).To(Succeed())

			By("Waiting for the FinalWarningThreshold to be exceeded")
			time.Sleep(testConfig().FinalWarningThreshold.Duration + time.Millisecond*200)

			By("Reconciling to trigger final warning")
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: janitorCRName(namespaceName), Namespace: namespaceName},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying final warning notification was sent")
			payloads := mockNotifier.GetPayloads()
			Expect(payloads).To(ContainElement(HaveField("ActionTaken", "FinalWarning")))

			By("Verifying final-warning label was applied")
			Eventually(func(g Gomega) {
				currentNS := &corev1.Namespace{}
				g.Expect(k8sClient.Get(ctx, nsKey, currentNS)).To(Succeed())
				g.Expect(currentNS.Labels).To(HaveKeyWithValue(FinalWarningLabelKey, "sent"))
			}, time.Second*5, time.Millisecond*250).Should(Succeed())

			By("Waiting for the DeleteThreshold to be exceeded")
			mockNotifier.Reset()
			time.Sleep(testConfig().DeleteThreshold.Duration - testConfig().FinalWarningThreshold.Duration + time.Millisecond*400)

			By("Reconciling to delete the namespace")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: janitorCRName(namespaceName), Namespace: namespaceName},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying deletion notification was sent")
			payloads = mockNotifier.GetPayloads()
			Expect(payloads).To(ContainElement(HaveField("ActionTaken", "DeletingNamespace")))

			By("Verifying the namespace is Terminating")
			Eventually(func(g Gomega) {
				terminatingNS := &corev1.Namespace{}
				g.Expect(k8sClient.Get(ctx, nsKey, terminatingNS)).To(Succeed())
				g.Expect(terminatingNS.DeletionTimestamp).NotTo(BeNil())
			}, time.Second*10, time.Millisecond*250).Should(Succeed())
		})

		It("should send NamespaceClaimed notification when team is assigned", func() {
			namespaceName := "test-e2e-claimed"
			nsKey := types.NamespacedName{Name: namespaceName}
			testNamespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: namespaceName,
					Labels: map[string]string{
						TeamLabelKey: TeamUnknown,
						FlagLabelKey: FlagYellow,
					},
					Annotations: map[string]string{
						RequesterAnnotationKey: "mohammadreza.saberi",
					},
				},
			}
			janitorCR := &snappcloudv1alpha1.NamespaceJanitor{
				ObjectMeta: metav1.ObjectMeta{
					Name:      janitorCRName(namespaceName),
					Namespace: namespaceName,
				},
			}
			Expect(k8sClient.Create(ctx, testNamespace)).To(Succeed())
			Expect(k8sClient.Create(ctx, janitorCR)).To(Succeed())

			By("Simulating the namespace being claimed by a team")
			currentNS := &corev1.Namespace{}
			Expect(k8sClient.Get(ctx, nsKey, currentNS)).To(Succeed())
			currentNS.Labels[TeamLabelKey] = "payments-team"
			Expect(k8sClient.Update(ctx, currentNS)).To(Succeed())

			By("Reconciling to trigger the cleanup logic")
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: janitorCRName(namespaceName), Namespace: namespaceName},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying the NamespaceClaimed notification was sent")
			payloads := mockNotifier.GetPayloads()
			Expect(payloads).To(ContainElement(SatisfyAll(
				HaveField("ActionTaken", "NamespaceClaimed"),
				HaveField("Requester", "mohammadreza.saberi"),
			)))

			By("Verifying the flag label has been removed")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, nsKey, currentNS)).To(Succeed())
				g.Expect(currentNS.Labels).ShouldNot(HaveKey(FlagLabelKey))
			}, time.Second*5, time.Millisecond*250).Should(Succeed())

			By("Verifying the CR status reflects TeamClaimed")
			crKey := types.NamespacedName{Name: janitorCRName(namespaceName), Namespace: namespaceName}
			updatedCR := &snappcloudv1alpha1.NamespaceJanitor{}
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, crKey, updatedCR)).To(Succeed())
				g.Expect(updatedCR.Status.Conditions).NotTo(BeEmpty())
				g.Expect(updatedCR.Status.Conditions[0].Reason).To(Equal("TeamClaimed"))
			}, time.Second*5, time.Millisecond*250).Should(Succeed())

			// Cleanup
			_ = k8sClient.Delete(ctx, testNamespace)
		})

		It("should send notification even when requester annotation is missing", func() {
			namespaceName := "test-e2e-no-requester"
			testNamespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:   namespaceName,
					Labels: map[string]string{TeamLabelKey: TeamUnknown},
				},
			}
			Expect(k8sClient.Create(ctx, testNamespace)).To(Succeed())
			Expect(k8sClient.Create(ctx, &snappcloudv1alpha1.NamespaceJanitor{
				ObjectMeta: metav1.ObjectMeta{Name: janitorCRName(namespaceName), Namespace: namespaceName},
			})).To(Succeed())

			By("Waiting for the YellowThreshold to be exceeded")
			time.Sleep(testConfig().YellowThreshold.Duration + time.Millisecond*200)

			By("Reconciling to apply the yellow flag")
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: janitorCRName(namespaceName), Namespace: namespaceName},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying the notification was sent with empty requester")
			payloads := mockNotifier.GetPayloads()
			Expect(payloads).To(ContainElement(SatisfyAll(
				HaveField("ActionTaken", "AppliedyellowFlag"),
				HaveField("Requester", ""),
			)))

			// Cleanup
			Expect(k8sClient.Delete(ctx, testNamespace)).To(Succeed())
		})
	})
})
