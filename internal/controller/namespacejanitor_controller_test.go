package controller

import (
	"context"
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

var _ = Describe("NamespaceJanitor Controller", func() {

	// Define constants for the test thresholds to make tests fast.
	const (
		testYellowThreshold = 2 * time.Second
		testRedThreshold    = 4 * time.Second
		testDeleteThreshold = 6 * time.Second
	)

	// Override the production constants for the duration of these tests.
	originalYellowThreshold := YellowThreshold
	originalRedThreshold := RedThreshold
	originalDeleteThreshold := DeleteThreshold

	BeforeEach(func() {
		YellowThreshold = testYellowThreshold
		RedThreshold = testRedThreshold
		DeleteThreshold = testDeleteThreshold
	})

	AfterEach(func() {
		YellowThreshold = originalYellowThreshold
		RedThreshold = originalRedThreshold
		DeleteThreshold = originalDeleteThreshold
	})

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
			Expect(result.RequeueAfter > 0).To(BeTrue())

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
			time.Sleep(testYellowThreshold + time.Millisecond*200)

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
			time.Sleep(testRedThreshold + time.Millisecond*200)

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

		It("should delete the namespace when the age exceeds DeleteThreshold", func() {
			namespaceName := "test-ns-delete"
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

			By("Waiting for the DeleteThreshold to be exceeded")
			time.Sleep(testDeleteThreshold + time.Millisecond*200)

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
})
