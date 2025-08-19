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
	RequesterAnnotationKey = "snappcloud.io/requester"
)

var (
	YellowThreshold = 1 * time.Minute //stage >> time.Minute * 1 production >> 7 * 24 * time.Hour
	RedThreshold    = 3 * time.Minute //stage >> time.Minute * 5 production >> 14 * 24 * time.Hour
	DeleteThreshold = 4 * time.Minute //stage >> time.Minute * 8 production >> 16 * 24 * time.Hour
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

// NamespaceJanitorReconciler reconciles a NamespaceJanitor object
type NamespaceJanitorReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=namespacejanitor.snappcloud.io,resources=namespacejanitors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=namespacejanitor.snappcloud.io,resources=namespacejanitors/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=namespacejanitor.snappcloud.io,resources=namespacejanitors/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;update;patch;delete
func janitorCRName(namespaceName string) string {
	return fmt.Sprintf("%s-janitor", namespaceName)
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/reconcile
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
			if !nsForCheck.ObjectMeta.DeletionTimestamp.IsZero() {
				logger.Info("Parent namespace is terminating. Skipping creation of default CR.", "namespace", namespaceName)
				return ctrl.Result{}, client.IgnoreNotFound(err)

			}
			if !isRelevent(&nsForCheck) {
				logger.Info("Namespace is no longer relevant. Skipping creation of default CR.", "namespace", namespaceName)
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
			return ctrl.Result{Requeue: true}, nil
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
			// If the NamespaceJanitor CR still exist but its Namespace is gone,
			// we might have to clean up the CR or update its status. For Now, we just stop.
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
		logger.Error(err, "failed to get target Namespace", "namespace", namespaceName)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Check if namespace is being deleted
	if !ns.ObjectMeta.DeletionTimestamp.IsZero() {
		logger.Info("Namespace is already in a terminating state. No action needed.", "namespace", ns.Name)
		return ctrl.Result{}, nil
	}

	if ns.Labels[TeamLabelKey] != TeamUnknown {
		if err := r.cleanupClaimedNamespace(ctx, &ns, &janitorCR, logger); err != nil {
			return ctrl.Result{RequeueAfter: time.Minute}, err
		}
		return ctrl.Result{}, nil
	}

	currentTeamLabel := ns.Labels[TeamLabelKey]
	currentFlagLabel := ns.Labels[FlagLabelKey]
	age := time.Since(ns.CreationTimestamp.Time)

	if age >= DeleteThreshold && currentFlagLabel == FlagRed {
		// --- Delete State ---
		logger.Info("Namespace has passed deletion threshold.", "namespace", ns.Name, "age", age.Round(time.Hour))
		if err := r.notifyAndDeleteNamespace(ctx, &ns, &janitorCR, logger); err != nil {
			logger.Error(err, "Failed to execute deletion process for namespace", "namespace", ns.Name)
			return ctrl.Result{RequeueAfter: 5 * time.Minute}, err // Retry deletion process soon
		}
	} else if age >= RedThreshold && currentFlagLabel == FlagYellow {
		// --- Red Flag State ---
		logger.Info("Namespace has passed red flag threshold.", "namespace", ns.Name, "age", age.Round(time.Hour))
		if err := r.notifyAndFlagNamespace(ctx, &ns, &janitorCR, FlagRed, logger); err != nil {
			logger.Error(err, "Failed to execute red flag process for namespace", "namespace", ns.Name)
			return ctrl.Result{RequeueAfter: time.Minute}, err // Retry red flag process soon
		}

	} else if age >= YellowThreshold && currentFlagLabel == "" {
		// --- Yellow Flag State ---
		logger.Info("Namespace has passed yellow flag threshold.", "namespace", ns.Name, "age", age.Round(time.Hour))
		if err := r.notifyAndFlagNamespace(ctx, &ns, &janitorCR, FlagYellow, logger); err != nil {
			logger.Error(err, "Failed to execute red flag process for namespace", "namespace", ns.Name)
			return ctrl.Result{RequeueAfter: time.Minute}, err // Retry red flag process soon
		}

	} else {
		logger.Info("No state transition required at this time.", "namespace", ns.Name, "currentFlag", currentFlagLabel, "age", age.Round(time.Hour))
	}

	var requeueAfter time.Duration
	// Recalculate the current flag label in case it was just changed.
	currentFlagLabel = ns.Labels[FlagLabelKey]
	if currentTeamLabel == TeamUnknown {
		switch currentFlagLabel {
		case "":
			requeueAfter = YellowThreshold - age
		case FlagYellow:
			requeueAfter = RedThreshold - age
		case FlagRed:
			requeueAfter = DeleteThreshold - age
		}
	}

	// If the next check is past due, requeue for the short interval to re-evaluate.
	// Imagine the operator was offline for two days and comes back online. requeueAfter = 7 days - 9 days = -2 days
	if requeueAfter <= 0 {
		// Check again soon if we're past a threshold but the state transition didn't happen for some reason.
		requeueAfter = 5 * time.Minute
	}
	logger.Info("Reconciliation finished, scheduling next check.", "after", requeueAfter.Round(time.Second))
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

func (r *NamespaceJanitorReconciler) notifyAndFlagNamespace(ctx context.Context, ns *corev1.Namespace, janitorCR *snappcloudv1alpha1.NamespaceJanitor, flag string, logger logr.Logger) error {
	action := fmt.Sprintf("Applied%sFlag", flag)
	// Idempotenty check: Dont do anything if the label is already correct.
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

	r.sendNotification(EventNotification{
		NamespaceName:        ns.Name,
		CurrentFlag:          flag,
		ActionTaken:          action,
		Age:                  time.Since(ns.CreationTimestamp.Time).String(),
		Requester:            ns.Annotations[RequesterAnnotationKey],
		AdditionalRecipients: janitorCR.Spec.AdditionalRecipients,
	}, logger)

	// Update the CR status if it exists
	if janitorCR.UID != "" {
		janitorCR.Status.LastFlagApplied = flag
		janitorCR.Status.Conditions = []metav1.Condition{
			{Type: "Managed", Status: metav1.ConditionTrue, Reason: action, Message: fmt.Sprintf("Flag %s applied.", flag), LastTransitionTime: metav1.Now()},
		}
		if err := r.Status().Update(ctx, janitorCR); err != nil {
			logger.Error(err, "Failed to update NamespaceJanitor status after applying flag")
			// Don't fail the whole operation for a status update error.
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

	// Use a patch to remove the label from the namespace.
	patch := client.MergeFrom(ns.DeepCopy())
	delete(ns.Labels, FlagLabelKey)

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

func (r *NamespaceJanitorReconciler) notifyAndDeleteNamespace(ctx context.Context, ns *corev1.Namespace, janitorCR *snappcloudv1alpha1.NamespaceJanitor, logger logr.Logger) error {
	logger.Info("Final notification before namespace deletion", "namespace", ns.Name)
	r.sendNotification(EventNotification{
		NamespaceName:        ns.Name,
		CurrentFlag:          FlagRed,
		ActionTaken:          "DeletingNamespace",
		Age:                  time.Since(ns.CreationTimestamp.Time).String(),
		Requester:            ns.Annotations[RequesterAnnotationKey],
		AdditionalRecipients: janitorCR.Spec.AdditionalRecipients,
	}, logger)

	logger.Info("Proceeding with namespace deletion", "namespace", ns.Name)
	if err := r.Delete(ctx, ns); err != nil {
		// If the namespace is already being terminated, IsNotFound can be returned.
		if !apierrors.IsNotFound(err) {
			logger.Error(err, "Failed to delete namespace", "namespace", ns.Name)
			return err
		}
	}

	logger.Info("Namespace deletion command issued successfully", "namespace", ns.Name)
	return nil
}

func (r *NamespaceJanitorReconciler) sendNotification(notification EventNotification, logger logr.Logger) {
	// In a real implementation, we request to NotificationCenter
	logger.Info("Sending notification",
		"namespace", notification.NamespaceName,
		"action", notification.ActionTaken,
		"flag", notification.CurrentFlag,
		"age", notification.Age,
		"requester", notification.Requester,
		"recipients", notification.AdditionalRecipients)
	// TODO: Implement actual notification sending
}

func isRelevent(obj client.Object) bool {
	if obj == nil {
		return false
	}
	labels := obj.GetLabels()
	if labels == nil {
		// A namespace without labels (like kube-public) is not relevant.
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
		// 1. watch for changes to NamespaceJanitor CRs
		For(&snappcloudv1alpha1.NamespaceJanitor{}).
		// 2. watch for changes to corev1.Namespaces objects
		Watches(
			&corev1.Namespace{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) []reconcile.Request {
				// `o` is the Namespace object that changed.
				// We create a reconcile request for the Janitor CR that *should* exist
				// in this namespace, using our predictable naming convention.
				return []reconcile.Request{
					{NamespacedName: types.NamespacedName{
						Name:      janitorCRName(o.GetName()),
						Namespace: o.GetName(),
					}},
				}
			}),
			// The predicate filters which Namespace events should trigger a reconciliation.
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				// This single function now acts as the gatekeeper for all events.
				labels := obj.GetLabels()

				// If an object (like openshift ns) has no labels or doesn't have our specific labels,
				// it will fail both checks below and be ignored.
				if labels == nil {
					return false
				}

				// Reconcile if the namespace is marked "unknown".
				// This handles the main lifecycle management (Create, and transitions "" -> yellow -> red).
				if labels[TeamLabelKey] == TeamUnknown {
					return true
				}

				// Reconcile if the namespace has one of our lifecycle flags.
				// This is crucial for the cleanup logic: when team changes from "unknown" to something else,
				// this check ensures we still process the event so we can remove the flag.
				if _, ok := labels[FlagLabelKey]; ok {
					return true
				}

				// If neither condition is met, it's not a namespace we care about.
				return false
			})),
		).
		Complete(r)
}
