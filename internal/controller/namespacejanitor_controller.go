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
	snappcloudv1alpha1 "github.com/rezacloner1372/namespacejanitor.git/api/v1alpha1"
)

const (
	TeamUnknown            = "unknown"
	FlagYellow             = "yellow"
	FlagRed                = "red"
	FlagLabelKey           = "snappcloud.io/flag"
	TeamLabelKey           = "snappcloud.io/team"
	RequesterAnnotationKey = "snappcloud.io/requester"

	YellowThreshold = time.Minute * 2 //production >> 7 * 24 * time.Hour
	RedThreshold    = time.Minute * 4 //production >> 14 * 24 * time.Hour
	DeleteThreshold = time.Minute * 6 //production >> 16 * 24 * time.Hour

	// Default name for the NamespaceJanitor CR if users create one.
	// The watch on Namespaces will enqueue a request for a CR with this name
	// in the namespace that changed.
	DefaultJanitorCRName = "default-janitor-config"
)

// EventNotification is a struct that represents a notification event(can be expanded)
type EventNotification struct {
	NamespaceName        string    `json:"namespace"`
	CurrentFlag          string    `json:"CurrentFlag"`
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

// +kubebuilder:rbac:groups=snappcloud.snappcloud.io,resources=namespacejanitors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=snappcloud.snappcloud.io,resources=namespacejanitors/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=snappcloud.snappcloud.io,resources=namespacejanitors/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the NamespaceJanitor object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/reconcile
func (r *NamespaceJanitorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("namespacejanitor", req.NamespacedName, "targetNamespace", req.Namespace)
	logger.Info("Reconciling started")

	var janitorCR snappcloudv1alpha1.NamespaceJanitor
	janitorCRExist := true
	if err := r.Get(ctx, req.NamespacedName, &janitorCR); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("NamespaceJanitor CR not found. Default logic will be applied to the namespace if relevant.", "crName", req.NamespacedName)
			janitorCRExist = false
			janitorCR.Spec = snappcloudv1alpha1.NamespaceJanitorSpec{}
		} else {
			logger.Error(err, "failed to get NamespaceJanitor CR")
			return ctrl.Result{}, err // Retry if it's not a not found error
		}
	}

	// Fetch the actual Namespace objects
	// This is the Namespace where the NamespaceJanitor CR (identified by req.NamespacedName) lives.
	namespaceName := req.Namespace
	var ns corev1.Namespace
	if err := r.Get(ctx, client.ObjectKey{Name: namespaceName}, &ns); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Target Namespace for this reconciliation cycle not found. It might have been deleted.", "namespace", namespaceName)
			// If the NamespaceJanitor CR still exist but its Namespace is gone,
			// we might have to clean up the CR or update its status. For Now, we just stop.
			return ctrl.Result{}, nil
		}
		logger.Error(err, "failed to get target Namespace", "namespace", namespaceName)
		return ctrl.Result{}, err
	}

	// --- core Logic: Check and flag the namespace ---
	currentTeamLabel := ns.Labels[TeamLabelKey]
	currentFlagLabel := ns.Labels[FlagLabelKey]
	age := time.Since(ns.CreationTimestamp.Time)

	// If the namespace is no longer "unknown", our job for this NS (via this trigger) might be done.
	// However, if it has one of our flags, we might still need to manage it (e.g. for Red flag or deletion).
	// For the initial goal, if it's not "unknown", we stop.
	if currentTeamLabel != TeamUnknown {
		// --- NEW CLEANUP LOGIC ---
		if err := r.cleanupClaimedNamespace(ctx, &ns, &janitorCR, janitorCRExist, logger); err != nil {
			// If cleanup fails, retry after a short interval.
			return ctrl.Result{RequeueAfter: time.Minute}, err
		}
		// Cleanup was successful, no need to requeue for this namespace.
		// We will only reconcile it again if its labels change in a way that matches our predicate.
		logger.Info("Cleanup of claimed namespace complete. No further action needed.")
		return ctrl.Result{}, nil // No requeue needed for this specefic namespace if it's claimed.
	}

	if age >= DeleteThreshold && currentFlagLabel == FlagRed {
		// --- Delete State ---
		logger.Info("Namespace has passed deletion threshold.", "namespace", ns.Name, "age", age.Round(time.Hour))
		if err := r.notifyAndDeleteNamespace(ctx, &ns, &janitorCR, logger); err != nil {
			logger.Error(err, "Failed to execute deletion process for namespace", "namespace", ns.Name)
			return ctrl.Result{RequeueAfter: 5 * time.Minute}, err // Retry deletion process soon
		}

		// Deletion was successful, update status ans stop reconciliation for this ns
		if janitorCRExist {
			janitorCR.Status.LastFlagApplied = "Deleted"
			// update status
			if err := r.Status().Update(ctx, &janitorCR); err != nil {
				logger.Error(err, "failed to update NamespaceJanitor CR status after deletion")
				// continue, dont block on status update
			}
		}
		return ctrl.Result{}, nil // Stop Reconciliation, the ns is gone
	} else if age >= RedThreshold && currentFlagLabel == FlagYellow {
		// --- Red Flag State
		logger.Info("Namespace has passed red flag threshold.", "namespace", ns.Name, "age", age.Round(time.Hour))
		if err := r.notifyAndFlagNamespace(ctx, &ns, &janitorCR, FlagRed, logger); err != nil {
			logger.Error(err, "Failed to execute red flag process for namespace", "namespace", ns.Name)
			return ctrl.Result{RequeueAfter: time.Minute}, err // Retry red flag process soon
		}

	} else if age >= YellowThreshold && currentFlagLabel == "" {
		// --- Yellow Flag State
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

	r.sendNotification(ctx, EventNotification{
		NamespaceName:        ns.Name,
		CurrentFlag:          flag,
		ActionTaken:          action,
		Age:                  time.Since(ns.CreationTimestamp.Time).String(),
		Requester:            ns.Annotations[RequesterAnnotationKey],
		AdditionalRecipients: janitorCR.Spec.AdditionalRecipients,
	}, logger)

	// Update the CR status if it exists
	if janitorCR.UID != "" { // A reliable way to check if the CR object is real
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
func (r *NamespaceJanitorReconciler) cleanupClaimedNamespace(ctx context.Context, ns *corev1.Namespace, janitorCR *snappcloudv1alpha1.NamespaceJanitor, janitorCRExists bool, logger logr.Logger) error {
	// Idempotency Check: If the flag label doesn't exist, there's nothing to do.
	if _, ok := ns.Labels[FlagLabelKey]; !ok {
		logger.Info("No flag label to clean up.", "namespace", ns.Name)
		// If the CR status still thinks a flag is applied, correct it.
		if janitorCRExists && janitorCR.Status.LastFlagApplied != "" {
			patch := client.MergeFrom(janitorCR.DeepCopy())
			janitorCR.Status.LastFlagApplied = ""
			janitorCR.Status.Conditions = []metav1.Condition{
				{Type: "Managed", Status: metav1.ConditionFalse, Reason: "TeamClaimed", Message: "Namespace has been claimed by a team; no longer managing.", LastTransitionTime: metav1.Now()},
			}
			return r.Status().Patch(ctx, janitorCR, patch)
		}
		return nil
	}

	logger.Info("Namespace has been claimed by a team. Cleaning up flag label.", "namespace", ns.Name, "team", ns.Labels[TeamLabelKey])
	patch := client.MergeFrom(ns.DeepCopy())
	delete(ns.Labels, FlagLabelKey) // Remove the label

	if err := r.Patch(ctx, ns, patch); err != nil {
		logger.Error(err, "Failed to remove flag label from claimed namespace", "namespace", ns.Name)
		return err
	}
	logger.Info("Successfully removed flag label.", "namespace", ns.Name)

	// Update the CR status if it exists
	if janitorCRExists {
		patch := client.MergeFrom(janitorCR.DeepCopy())
		janitorCR.Status.LastFlagApplied = "" // Clear the flag status
		janitorCR.Status.Conditions = []metav1.Condition{
			{Type: "Managed", Status: metav1.ConditionFalse, Reason: "TeamClaimed", Message: "Namespace has been claimed by a team; flag removed.", LastTransitionTime: metav1.Now()},
		}
		if err := r.Status().Patch(ctx, janitorCR, patch); err != nil {
			logger.Error(err, "Failed to update NamespaceJanitor status after cleanup")
			// Don't fail the whole operation for a status update error.
		}
	}

	return nil
}

func (r *NamespaceJanitorReconciler) notifyAndDeleteNamespace(ctx context.Context, ns *corev1.Namespace, janitorCR *snappcloudv1alpha1.NamespaceJanitor, logger logr.Logger) error {
	logger.Info("Final notification before namespace deletion", "namespace", ns.Name)
	r.sendNotification(ctx, EventNotification{
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

// sendNotification is a placeholder for actual notification logic
func (r *NamespaceJanitorReconciler) sendNotification(ctx context.Context, notification EventNotification, logger logr.Logger) {
	// In a real implementation, this would integrate with an email service, Slack, etc.
	logger.Info("Sending notification",
		"namespace", notification.NamespaceName,
		"action", notification.ActionTaken,
		"flag", notification.CurrentFlag,
		"age", notification.Age,
		"requester", notification.Requester,
		"recipients", notification.AdditionalRecipients)
	// TODO: Implement actual notification sending
}

// SetupWithManager sets up the controller with the Manager.
func (r *NamespaceJanitorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		// 1. watch for changes to NamespaceJanitor CRs
		For(&snappcloudv1alpha1.NamespaceJanitor{}).
		// 2. watch for changes to corev1.Namespaces objects
		Watches(
			&corev1.Namespace{}, // Source: watching Namespace objects
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) []reconcile.Request {

				return []reconcile.Request{
					{NamespacedName: types.NamespacedName{
						Name:      DefaultJanitorCRName, // e.g., "default-janitor-config"
						Namespace: o.GetName(),          // The namespace where the CR should live
					}},
				}
			}),
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				// `obj` is a Namespace.
				// Only trigger if the namespace is relevant to our logic.
				labels := obj.GetLabels()
				// Trigger if team is unknown AND it's not yet flagged yellow (or whatever our initial target flag is)
				if labels[TeamLabelKey] == TeamUnknown && labels[FlagLabelKey] != FlagYellow {
					return true
				}
				// Or trigger if it has our team label and has one of our flags (for state transitions or cleanup)
				// For the initial goal, this might be too broad, but good for future expansion.
				// if labels[TeamLabelKey] == TeamUnknown && labels[FlagLabelKey] != "" {
				// return true
				// }
				return false
			})),
		).
		Complete(r)
}
