/*
Copyright 2025 mohammadreza.saberi@snapp.cab.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/snapp-incubator/namespacejanitor/internal/notification"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"
	snappcloudv1alpha1 "github.com/snapp-incubator/namespacejanitor/api/v1alpha1"
)

const (
	TeamUnknown            = "unknown"
	FlagYellow             = "yellow"
	FlagRed                = "red"
	FlagLabelKey           = "snappcloud.io/flag"
	TeamLabelKey           = "snappcloud.io/team"
	RequesterAnnotationKey = "openshift.io/requester"

	// FinalWarningLabelKey tracks whether the final warning notification has been sent.
	// Value "sent" means the warning was delivered; the operator will not re-send it.
	FinalWarningLabelKey = "snappcloud.io/final-warning"

	// CreationNotifiedLabelKey tracks whether the creation notification has been sent.
	// Value "true" means the creation notification was delivered; the operator will not re-send it.
	CreationNotifiedLabelKey = "snappcloud.io/creation-notified"

	// ScaledDownLabelKey tracks whether the operator has already scaled
	// every workload in this namespace to zero. The terminal state for
	// the first phase — the namespace itself is preserved, but no
	// controller will re-apply the scale-down once this label is set.
	ScaledDownLabelKey = "snappcloud.io/scaled-down"
)

// EventNotification is a struct that represents a notification event(can be expanded)
type EventNotification struct {
	NamespaceName        string    `json:"namespace"`
	CurrentFlag          string    `json:"currentFlag"`
	ActionTaken          string    `json:"actionTaken"`
	Age                  string    `json:"age"`
	CreationTimestamp    time.Time `json:"creationTimestamp"`
	Requester            string    `json:"requester"`
	AdditionalRecipients []string  `json:"additionalRecipients"`
}

type NamespaceJanitorReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Notifier notification.Notifier
	Config   LifecycleConfig
	Excluder *NamespaceExcluder
	Region   string
}

// +kubebuilder:rbac:groups=namespacejanitor.snappcloud.io,resources=namespacejanitors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=namespacejanitor.snappcloud.io,resources=namespacejanitors/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=namespacejanitor.snappcloud.io,resources=namespacejanitors/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;update;patch
// Final-state action: scale workloads (Deployments, StatefulSets,
// ReplicaSets, CronJobs) to zero. The namespace itself is preserved so
// a team can still claim and manually restore it later.
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=apps,resources=replicasets,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=batch,resources=cronjobs,verbs=get;list;watch;update;patch

func janitorCRName(namespaceName string) string {
	return fmt.Sprintf("%s-janitor", namespaceName)
}

func (r *NamespaceJanitorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling started")

	namespaceName := req.Namespace
	var janitorCR snappcloudv1alpha1.NamespaceJanitor
	if err := r.Get(ctx, req.NamespacedName, &janitorCR); err != nil {
		if apierrors.IsNotFound(err) {
			var nsForCheck corev1.Namespace
			if err := r.Get(ctx, client.ObjectKey{Name: namespaceName}, &nsForCheck); err != nil {
				logger.Info("Parent namespace not found while attempting to create default CR. Assuming it was deleted.", "namespace", namespaceName)
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}
			if !nsForCheck.DeletionTimestamp.IsZero() {
				logger.Info("Parent namespace is terminating. Skipping creation of default CR.", "namespace", namespaceName)
				return ctrl.Result{}, client.IgnoreNotFound(err)

			}
			if !isRelevant(&nsForCheck) {
				logger.Info("Namespace is no longer relevant. Skipping creation of default CR.", "namespace", namespaceName)
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}
			if r.Excluder != nil && r.Excluder.IsExcluded(namespaceName) {
				logger.Info("Namespace matches exclusion pattern. Skipping creation of default CR.", "namespace", namespaceName)
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}

			// Create the default CR
			logger.Info("NamespaceJanitor CR not found. Creating a default one.", "namespace", namespaceName)
			defaultCR := &snappcloudv1alpha1.NamespaceJanitor{
				ObjectMeta: metav1.ObjectMeta{
					Name:      req.Name,
					Namespace: req.Namespace,
				},
				Spec: snappcloudv1alpha1.NamespaceJanitorSpec{
					AdditionalRecipients: []string{},
				},
			}
			if err := r.Create(ctx, defaultCR); err != nil {
				logger.Error(err, "Failed to create default NamespaceJanitor CR", "namespace", namespaceName)
				return ctrl.Result{}, err
			}
			logger.Info("Successfully created default NamespaceJanitor CR.", "namespace", namespaceName)
			return ctrl.Result{RequeueAfter: time.Second}, nil
		}
		logger.Error(err, "Failed to get NamespaceJanitor CR")
		return ctrl.Result{}, err
	}

	// === CR EXISTS, PROCEED WITH MAIN LOGIC ===
	// Fetch the actual Namespace object
	var ns corev1.Namespace
	if err := r.Get(ctx, client.ObjectKey{Name: namespaceName}, &ns); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Target Namespace for this reconciliation cycle not found. It might have been deleted.", "namespace", namespaceName)
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
		logger.Error(err, "failed to get target Namespace", "namespace", namespaceName)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Check if namespace is being deleted
	if !ns.DeletionTimestamp.IsZero() {
		logger.Info("Namespace is already in a terminating state. No action needed.", "namespace", ns.Name)
		return ctrl.Result{}, nil
	}

	// Check exclusion list
	if r.Excluder != nil && r.Excluder.IsExcluded(ns.Name) {
		logger.Info("Namespace matches exclusion pattern. Skipping.", "namespace", ns.Name)
		return ctrl.Result{}, nil
	}

	if ns.Labels[TeamLabelKey] != TeamUnknown {
		if err := r.cleanupClaimedNamespace(ctx, &ns, &janitorCR, logger); err != nil {
			return ctrl.Result{RequeueAfter: time.Minute}, err
		}
		return ctrl.Result{}, nil
	}

	currentFlagLabel := ns.Labels[FlagLabelKey]
	age := time.Since(ns.CreationTimestamp.Time)

	// === CREATION NOTIFICATION (idempotent) ===
	if r.Config.CreationNotification && ns.Labels[CreationNotifiedLabelKey] != "true" {
		logger.Info("Sending creation notification for newly seen unowned namespace.", "namespace", ns.Name)
		r.sendNotification(ctx, notification.JanitorPayload{
			NamespaceName:        ns.Name,
			CurrentFlag:          currentFlagLabel,
			ActionTaken:          "NamespaceCreated",
			Age:                  notification.FormatAge(age),
			Requester:            ns.Annotations[RequesterAnnotationKey],
			AdditionalRecipients: janitorCR.Spec.AdditionalRecipients,
			Region:               r.Region,
		}, logger)
		// Mark as notified to ensure idempotency
		patch := client.MergeFrom(ns.DeepCopy())
		if ns.Labels == nil {
			ns.Labels = make(map[string]string)
		}
		ns.Labels[CreationNotifiedLabelKey] = "true"
		if err := r.Patch(ctx, &ns, patch); err != nil {
			logger.Error(err, "Failed to mark creation notification as sent", "namespace", ns.Name)
		}
	}

	// === LIFECYCLE STATE MACHINE ===
	// Order: ScaleDown > FinalWarning > Red > Yellow
	// Each branch is idempotent: it checks current state before transitioning.

	if age >= r.Config.DeleteThreshold.Duration && ns.Labels[FinalWarningLabelKey] == "sent" {
		// --- Scale-Down State ---
		logger.Info("Namespace has passed deletion threshold.", "namespace", ns.Name, "age", age.Round(time.Hour))
		if err := r.notifyAndScaleDownNamespace(ctx, &ns, &janitorCR, logger); err != nil {
			logger.Error(err, "Failed to execute scale-down process for namespace", "namespace", ns.Name)
			return ctrl.Result{RequeueAfter: 5 * time.Minute}, err
		}
	} else if age >= r.Config.FinalWarningThreshold.Duration && currentFlagLabel == FlagRed && ns.Labels[FinalWarningLabelKey] != "sent" {
		// --- Final Warning State ---
		logger.Info("Namespace has passed final warning threshold.", "namespace", ns.Name, "age", age.Round(time.Hour))
		if err := r.sendFinalWarning(ctx, &ns, &janitorCR, logger); err != nil {
			logger.Error(err, "Failed to send final warning for namespace", "namespace", ns.Name)
			return ctrl.Result{RequeueAfter: time.Minute}, err
		}
	} else if age >= r.Config.RedThreshold.Duration && currentFlagLabel == FlagYellow {
		// --- Red Flag State ---
		logger.Info("Namespace has passed red flag threshold.", "namespace", ns.Name, "age", age.Round(time.Hour))
		if err := r.notifyAndFlagNamespace(ctx, &ns, &janitorCR, FlagRed, logger); err != nil {
			logger.Error(err, "Failed to execute red flag process for namespace", "namespace", ns.Name)
			return ctrl.Result{RequeueAfter: time.Minute}, err
		}
	} else if age >= r.Config.YellowThreshold.Duration && currentFlagLabel == "" {
		// --- Yellow Flag State ---
		logger.Info("Namespace has passed yellow flag threshold.", "namespace", ns.Name, "age", age.Round(time.Hour))
		if err := r.notifyAndFlagNamespace(ctx, &ns, &janitorCR, FlagYellow, logger); err != nil {
			logger.Error(err, "Failed to execute yellow flag process for namespace", "namespace", ns.Name)
			return ctrl.Result{RequeueAfter: time.Minute}, err
		}
	} else {
		logger.Info("No state transition required at this time.", "namespace", ns.Name, "currentFlag", currentFlagLabel, "age", age.Round(time.Hour))
	}

	// === REQUEUE FOR NEXT TRANSITION ===
	// Re-read labels in case we just changed them.
	currentFlagLabel = ns.Labels[FlagLabelKey]
	requeueAfter := r.computeNextRequeue(currentFlagLabel, age)

	if requeueAfter <= 0 {
		requeueAfter = 5 * time.Minute
	}
	logger.Info("Reconciliation finished, scheduling next check.", "after", requeueAfter.Round(time.Second))
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// computeNextRequeue returns the duration until the next lifecycle transition.
func (r *NamespaceJanitorReconciler) computeNextRequeue(flag string, age time.Duration) time.Duration {
	switch flag {
	case "":
		return r.Config.YellowThreshold.Duration - age
	case FlagYellow:
		return r.Config.RedThreshold.Duration - age
	case FlagRed:
		return r.Config.FinalWarningThreshold.Duration - age
	default:
		// final-warning or unknown: requeue until delete
		return r.Config.DeleteThreshold.Duration - age
	}
}

func (r *NamespaceJanitorReconciler) notifyAndFlagNamespace(ctx context.Context, ns *corev1.Namespace, janitorCR *snappcloudv1alpha1.NamespaceJanitor, flag string, logger logr.Logger) error {
	action := fmt.Sprintf("Applied%sFlag", flag)
	if ns.Labels[FlagLabelKey] == flag {
		logger.Info("Namespace already has the correct flag label.", "namespace", ns.Name, "flag", flag)
		return nil
	}
	patch := client.MergeFrom(ns.DeepCopy())
	if ns.Labels == nil {
		ns.Labels = make(map[string]string)
	}
	ns.Labels[FlagLabelKey] = flag
	logger.Info("Applying flag label to namespace", "namespace", ns.Name, "flag", flag)
	if err := r.Patch(ctx, ns, patch); err != nil {
		logger.Error(err, "Failed to apply flag label to namespace", "namespace", ns.Name, "flag", flag)
		return err
	}

	r.sendNotification(ctx, notification.JanitorPayload{
		NamespaceName:        ns.Name,
		CurrentFlag:          flag,
		ActionTaken:          action,
		Age:                  notification.FormatAge(time.Since(ns.CreationTimestamp.Time)),
		Requester:            ns.Annotations[RequesterAnnotationKey],
		AdditionalRecipients: janitorCR.Spec.AdditionalRecipients,
		Region:               r.Region,
	}, logger)

	// Update the CR status if it exists
	if janitorCR.UID != "" {
		janitorCR.Status.LastFlagApplied = flag
		janitorCR.Status.Conditions = []metav1.Condition{
			{Type: "Managed", Status: metav1.ConditionTrue, Reason: action, Message: fmt.Sprintf("Flag %s applied.", flag), LastTransitionTime: metav1.Now()},
		}
		if err := r.Status().Update(ctx, janitorCR); err != nil {
			logger.Error(err, "Failed to update NamespaceJanitor status after applying flag")
		}
	}

	return nil
}

// sendFinalWarning sends the final warning notification and labels the namespace
// so the notification is not re-sent.
func (r *NamespaceJanitorReconciler) sendFinalWarning(ctx context.Context, ns *corev1.Namespace, janitorCR *snappcloudv1alpha1.NamespaceJanitor, logger logr.Logger) error {
	logger.Info("Sending final warning notification.", "namespace", ns.Name)

	r.sendNotification(ctx, notification.JanitorPayload{
		NamespaceName:        ns.Name,
		CurrentFlag:          FlagRed,
		ActionTaken:          "FinalWarning",
		Age:                  notification.FormatAge(time.Since(ns.CreationTimestamp.Time)),
		Requester:            ns.Annotations[RequesterAnnotationKey],
		AdditionalRecipients: janitorCR.Spec.AdditionalRecipients,
		Region:               r.Region,
	}, logger)

	// Label the namespace so we never re-send this warning.
	patch := client.MergeFrom(ns.DeepCopy())
	if ns.Labels == nil {
		ns.Labels = make(map[string]string)
	}
	ns.Labels[FinalWarningLabelKey] = "sent"
	if err := r.Patch(ctx, ns, patch); err != nil {
		logger.Error(err, "Failed to apply final-warning label", "namespace", ns.Name)
		return err
	}

	// Update CR status
	if janitorCR.UID != "" {
		janitorCR.Status.LastFlagApplied = string(StageFinalWarning)
		janitorCR.Status.Conditions = []metav1.Condition{
			{Type: "Managed", Status: metav1.ConditionTrue, Reason: "FinalWarning", Message: "Final warning sent. Workloads will be scaled to zero within 24 hours.", LastTransitionTime: metav1.Now()},
		}
		if err := r.Status().Update(ctx, janitorCR); err != nil {
			logger.Error(err, "Failed to update NamespaceJanitor status after final warning")
		}
	}

	return nil
}

// cleanupClaimedNamespace is called when a namespace's team label is no longer "unknown".
// It removes the lifecycle flag and updates the status of the corresponding Janitor CR.
func (r *NamespaceJanitorReconciler) cleanupClaimedNamespace(ctx context.Context, ns *corev1.Namespace, janitorCR *snappcloudv1alpha1.NamespaceJanitor, logger logr.Logger) error {
	// Idempotency Check: If the flag label doesn't exist, there's nothing to do.
	if _, ok := ns.Labels[FlagLabelKey]; !ok {
		logger.Info("No flag label to clean up on this claimed namespace.", "namespace", ns.Name)
		return nil
	}

	logger.Info("Namespace has been claimed by a team. Cleaning up flag label.", "namespace", ns.Name, "team", ns.Labels[TeamLabelKey])

	currentFlag := ns.Labels[FlagLabelKey]
	r.sendNotification(ctx, notification.JanitorPayload{
		NamespaceName:        ns.Name,
		CurrentFlag:          currentFlag,
		ActionTaken:          "NamespaceClaimed",
		Age:                  notification.FormatAge(time.Since(ns.CreationTimestamp.Time)),
		Requester:            ns.Annotations[RequesterAnnotationKey],
		AdditionalRecipients: janitorCR.Spec.AdditionalRecipients,
		Region:               r.Region,
	}, logger)

	// Use a patch to remove the label from the namespace.
	patch := client.MergeFrom(ns.DeepCopy())
	delete(ns.Labels, FlagLabelKey)
	delete(ns.Labels, FinalWarningLabelKey)
	delete(ns.Labels, CreationNotifiedLabelKey)

	if err := r.Patch(ctx, ns, patch); err != nil {
		logger.Error(err, "Failed to remove flag label from claimed namespace", "namespace", ns.Name)
		return err
	}
	logger.Info("Successfully removed flag label.", "namespace", ns.Name)

	// Now, update the status on the NamespaceJanitor CR to reflect it's no longer managing.
	statusPatch := client.MergeFrom(janitorCR.DeepCopy())
	janitorCR.Status.LastFlagApplied = "CleanedUp"
	Message := "Namespace has been claimed by team " + ns.Labels[TeamLabelKey] + " so lifecycle management is complete"
	janitorCR.Status.Conditions = []metav1.Condition{
		{
			Type:               "Managed",
			Status:             metav1.ConditionFalse,
			Reason:             "TeamClaimed",
			Message:            Message,
			LastTransitionTime: metav1.Now(),
		},
	}
	if err := r.Status().Patch(ctx, janitorCR, statusPatch); err != nil {
		logger.Error(err, "Failed to update NamespaceJanitor status after cleanup")
	}

	return nil
}

// notifyAndScaleDownNamespace scales every workload in the namespace to
// zero (idempotently — already-zero workloads are skipped) and, on the
// first invocation only, sends the final notification, applies the
// scaled-down label, and updates the NamespaceJanitor CR status.
//
// Scaling itself runs on every reconcile, even after the scaled-down
// label has been applied. This is deliberate: users may re-create
// workloads in a scaled-down namespace, and we must keep driving them
// back to zero until the namespace is either claimed or cleaned up
// manually.
func (r *NamespaceJanitorReconciler) notifyAndScaleDownNamespace(ctx context.Context, ns *corev1.Namespace, janitorCR *snappcloudv1alpha1.NamespaceJanitor, logger logr.Logger) error {
	alreadyNotified := ns.Labels[ScaledDownLabelKey] == "true"

	logger.Info("Scaling workloads to zero", "namespace", ns.Name, "alreadyNotified", alreadyNotified)
	if err := r.scaleDownWorkloads(ctx, ns.Name, logger); err != nil {
		logger.Error(err, "Failed to scale workloads to zero", "namespace", ns.Name)
		return fmt.Errorf("scaling workloads in namespace %s: %w", ns.Name, err)
	}

	if alreadyNotified {
		// Notification was already sent in a previous reconcile. We only
		// re-enforce the zero-replica state above.
		return nil
	}

	logger.Info("Final notification before scale-down", "namespace", ns.Name)
	r.sendNotification(ctx, notification.JanitorPayload{
		NamespaceName:        ns.Name,
		CurrentFlag:          FlagRed,
		ActionTaken:          "ScalingDownWorkloads",
		Age:                  notification.FormatAge(time.Since(ns.CreationTimestamp.Time)),
		Requester:            ns.Annotations[RequesterAnnotationKey],
		AdditionalRecipients: janitorCR.Spec.AdditionalRecipients,
		Region:               r.Region,
	}, logger)

	// Mark the namespace as notified so subsequent reconciles don't
	// re-send the notification. Scaling still runs on every reconcile.
	patch := client.MergeFrom(ns.DeepCopy())
	if ns.Labels == nil {
		ns.Labels = make(map[string]string)
	}
	ns.Labels[ScaledDownLabelKey] = "true"
	if err := r.Patch(ctx, ns, patch); err != nil {
		logger.Error(err, "Failed to apply scaled-down label", "namespace", ns.Name)
		return fmt.Errorf("applying scaled-down label: %w", err)
	}

	// Update CR status to reflect the terminal state.
	if janitorCR.UID != "" {
		janitorCR.Status.LastFlagApplied = string(StageScaledDown)
		janitorCR.Status.Conditions = []metav1.Condition{
			{Type: "Managed", Status: metav1.ConditionTrue, Reason: "ScalingDownWorkloads", Message: "All workloads scaled to zero. Namespace preserved.", LastTransitionTime: metav1.Now()},
		}
		if err := r.Status().Update(ctx, janitorCR); err != nil {
			logger.Error(err, "Failed to update NamespaceJanitor status after scale-down")
		}
	}

	logger.Info("Namespace scale-down completed", "namespace", ns.Name)
	return nil
}

// scaleDownWorkloads patches every Deployments/StatefulSets/ReplicaSets
// in the namespace to replicas=0 and every CronJob to suspend=true.
// Already-scaled workloads are skipped to keep the operation idempotent.
// DaemonSets and active Jobs are intentionally left untouched — DaemonSets
// can't be cleanly scaled to zero, and Jobs are one-shot so scaling them
// has no effect on the cluster.
func (r *NamespaceJanitorReconciler) scaleDownWorkloads(ctx context.Context, namespaceName string, logger logr.Logger) error {
	zero := int32(0)
	suspend := true

	// --- Deployments ---
	var deployments appsv1.DeploymentList
	if err := r.List(ctx, &deployments, client.InNamespace(namespaceName)); err != nil {
		return fmt.Errorf("listing Deployments: %w", err)
	}
	for i := range deployments.Items {
		d := &deployments.Items[i]
		if d.Spec.Replicas != nil && *d.Spec.Replicas == 0 {
			continue
		}
		patch := client.MergeFrom(d.DeepCopy())
		d.Spec.Replicas = &zero
		if err := r.Patch(ctx, d, patch); err != nil {
			return fmt.Errorf("scaling Deployment %s/%s to zero: %w", namespaceName, d.Name, err)
		}
		logger.Info("Scaled Deployment to zero", "namespace", namespaceName, "deployment", d.Name)
	}

	// --- StatefulSets ---
	var statefulsets appsv1.StatefulSetList
	if err := r.List(ctx, &statefulsets, client.InNamespace(namespaceName)); err != nil {
		return fmt.Errorf("listing StatefulSets: %w", err)
	}
	for i := range statefulsets.Items {
		ss := &statefulsets.Items[i]
		if ss.Spec.Replicas != nil && *ss.Spec.Replicas == 0 {
			continue
		}
		patch := client.MergeFrom(ss.DeepCopy())
		ss.Spec.Replicas = &zero
		if err := r.Patch(ctx, ss, patch); err != nil {
			return fmt.Errorf("scaling StatefulSet %s/%s to zero: %w", namespaceName, ss.Name, err)
		}
		logger.Info("Scaled StatefulSet to zero", "namespace", namespaceName, "statefulset", ss.Name)
	}

	// --- ReplicaSets (orphaned, not owned by a Deployment) ---
	var replicasets appsv1.ReplicaSetList
	if err := r.List(ctx, &replicasets, client.InNamespace(namespaceName)); err != nil {
		return fmt.Errorf("listing ReplicaSets: %w", err)
	}
	for i := range replicasets.Items {
		rs := &replicasets.Items[i]
		if rs.Spec.Replicas != nil && *rs.Spec.Replicas == 0 {
			continue
		}
		patch := client.MergeFrom(rs.DeepCopy())
		rs.Spec.Replicas = &zero
		if err := r.Patch(ctx, rs, patch); err != nil {
			return fmt.Errorf("scaling ReplicaSet %s/%s to zero: %w", namespaceName, rs.Name, err)
		}
		logger.Info("Scaled ReplicaSet to zero", "namespace", namespaceName, "replicaset", rs.Name)
	}

	// --- CronJobs ---
	var cronjobs batchv1.CronJobList
	if err := r.List(ctx, &cronjobs, client.InNamespace(namespaceName)); err != nil {
		return fmt.Errorf("listing CronJobs: %w", err)
	}
	for i := range cronjobs.Items {
		cj := &cronjobs.Items[i]
		if cj.Spec.Suspend != nil && *cj.Spec.Suspend {
			continue
		}
		patch := client.MergeFrom(cj.DeepCopy())
		cj.Spec.Suspend = &suspend
		if err := r.Patch(ctx, cj, patch); err != nil {
			return fmt.Errorf("suspending CronJob %s/%s: %w", namespaceName, cj.Name, err)
		}
		logger.Info("Suspended CronJob", "namespace", namespaceName, "cronjob", cj.Name)
	}

	return nil
}

func (r *NamespaceJanitorReconciler) sendNotification(ctx context.Context, payload notification.JanitorPayload, logger logr.Logger) {
	if r.Notifier == nil {
		logger.Info("Notifier is not configured, skipping notification.", "namespace", payload.NamespaceName)
		return
	}

	logger.Info("Requesting to send notification",
		"namespace", payload.NamespaceName,
		"action", payload.ActionTaken,
		"flag", payload.CurrentFlag,
	)

	if err := r.Notifier.Send(payload); err != nil {
		logger.Error(err, "Failed to send notification")
	}
}

func isRelevant(obj client.Object) bool {
	if obj == nil {
		return false
	}
	labels := obj.GetLabels()
	if labels == nil {
		return false
	}

	// The namespace is actively being managed because its team is 'unknown'.
	if labels[TeamLabelKey] == TeamUnknown {
		return true
	}

	// The namespace is NOT 'unknown', but it still has our flag.
	// This makes it relevant for cleanup.
	if _, ok := labels[FlagLabelKey]; ok {
		return true
	}

	// If neither of the above, we don't care about it.
	return false
}

// SetupWithManager sets up the controller with the Manager.
func (r *NamespaceJanitorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&snappcloudv1alpha1.NamespaceJanitor{}).
		Watches(
			&corev1.Namespace{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) []reconcile.Request {
				return []reconcile.Request{
					{NamespacedName: types.NamespacedName{
						Name:      janitorCRName(o.GetName()),
						Namespace: o.GetName(),
					}},
				}
			}),
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				// Exclude namespaces matching exclusion patterns
				if r.Excluder != nil && r.Excluder.IsExcluded(obj.GetName()) {
					return false
				}
				labels := obj.GetLabels()
				if labels == nil {
					return false
				}
				if labels[TeamLabelKey] == TeamUnknown {
					return true
				}
				if _, ok := labels[FlagLabelKey]; ok {
					return true
				}
				return false
			})),
		).
		Complete(r)
}
