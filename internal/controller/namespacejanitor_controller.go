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
	NamespaceName        string            `json:"namespace"`
	CurrentFlag          map[string]string `json:"CurrentFlag"`
	ActionTaken          string            `json:"actionTaken"`
	Age                  string            `json:"age"`
	CreationTimestamp    time.Time         `json:"creationTimestamp"`
	Requester            string            `json:"requester"`
	AdditionalRecipients []string          `json:"additionalRecipients"`
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
		logger.Info("Namespace is no longer unknown. No further action is needed.", "namespace", namespaceName, "currentTeamLabel", currentTeamLabel)
		// If a flag was previously applied by us, consider removing it or updating status.
		// For now, simple stop.
		if janitorCRExist && janitorCR.Status.LastFlagApplied != "" {
			janitorCR.Status.LastFlagApplied = "N/A (team claimed)"
			janitorCR.Status.Conditions = []metav1.Condition{
				{Type: "Managed", Status: metav1.ConditionFalse, Reason: "TeamClaimed", Message: "Namespace team label is no longer unknown."},
			}
			if err := r.Status().Update(ctx, &janitorCR); err != nil {
				logger.Error(err, "failed to update NamespaceJanitor CR status after team claimed")
				// continue, dont block on status update
			}
		}
		return ctrl.Result{}, nil // No requeue needed for this specefic namespace if it's claimed.
	}

	// State transaction logic
	var desiredFlag string
	var actionTaken string

	if age >= YellowThreshold && currentFlagLabel != FlagYellow {
		// Only apply if it's not already yellow
		// This also handles the case where currentFlagLabel is empty (no flag applied yet)
		desiredFlag = FlagYellow
		actionTaken = "AppliedYellowFlag"
	}

	// else if age >= RedThreshold && currentFlagLabel == FlagYellow {
	// desiredFlag = FlagRed
	// actionTaken = "AppliedRedFlag"
	// }
	if desiredFlag != "" && currentFlagLabel != desiredFlag {
		logger.Info("Applying flag", "namespace", namespaceName, "desiredFlag", desiredFlag, "currentFlagLabel", currentFlagLabel, "age", age.Round(time.Hour))
		if ns.Labels == nil {
			ns.Labels = make(map[string]string)
		}
		originalLabels := client.MergeFrom(ns.DeepCopy()) // for patch
		ns.Labels[FlagLabelKey] = desiredFlag
		if err := r.Patch(ctx, &ns, originalLabels); err != nil {
			logger.Error(err, "failed to patch namespace labels", "namespace", namespaceName)
			return ctrl.Result{RequeueAfter: time.Minute}, err //use requeue to retry on failure
		}
		logger.Info("Successfully applied flag", "namespace", namespaceName, "desiredFlag", desiredFlag)

		// Send a notification to the team
		r.sendNotification(ctx, EventNotification{
			NamespaceName:        ns.Name,
			CurrentFlag:          map[string]string{FlagLabelKey: desiredFlag},
			ActionTaken:          actionTaken,
			Age:                  age.Round(time.Hour).String(),
			CreationTimestamp:    ns.CreationTimestamp.Time,
			Requester:            ns.Annotations[RequesterAnnotationKey],
			AdditionalRecipients: janitorCR.Spec.AdditionalRecipients, // will be empty if CR doesnt exist
		}, logger)

		if janitorCRExist {
			janitorCR.Status.LastFlagApplied = desiredFlag
			janitorCR.Status.LastNotificationSent = &metav1.Time{Time: time.Now()}
			janitorCR.Status.Conditions = []metav1.Condition{
				{Type: "Managed", Status: metav1.ConditionTrue, Reason: actionTaken, Message: fmt.Sprintf("Flag %s applied to namespace.", desiredFlag), LastTransitionTime: metav1.Now()},
			}
			if err := r.Status().Update(ctx, &janitorCR); err != nil {
				logger.Error(err, "Failed to update NamespaceJanitor status after applying flag")
				// Don't let status update failure block main logic, but log it.
			}
		}
	} else if desiredFlag == "" && currentFlagLabel != "" {
		logger.Info("Namespace is currently flagged but does not meet criteria for new flag.", "namespace", ns.Name, "currentFlag", currentFlagLabel)
	} else {
		logger.Info("No flag action needed for namespace at this time.", "namespace", ns.Name, "currentFlag", currentFlagLabel, "age", age.Round(time.Hour))
	}
	// calculate next requeue time
	var requeueAfter time.Duration
	if currentTeamLabel == TeamUnknown {
		if currentFlagLabel != FlagYellow && age < YellowThreshold {
			requeueAfter = YellowThreshold - age
		} else {
			// If it's already yellow, or past yellow threshold and still unknown,
			// check periodically (e.g., daily) in case thresholds change or for future states.
			requeueAfter = 3 * time.Minute // Or a shorter interval if more states are imminent
		}
	}
	if requeueAfter <= 0 {
		requeueAfter = 5 * time.Minute // Check soon
	}

	// Ensure a minimum requeue time to avoid excessive API calls
	minRequeue := 1 * time.Minute
	if requeueAfter < minRequeue && requeueAfter > 0 {
		requeueAfter = minRequeue
	}

	if requeueAfter > 0 {
		logger.Info("Scheduling next reconciliation check", "after", requeueAfter.Round(time.Second))
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}

	logger.Info("Reconciliation finished, no specific requeue scheduled by age logic.")
	return ctrl.Result{}, nil
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
				// `o` is the Namespace object that changed.
				// We want to trigger reconciliation for a NamespaceJanitor CR
				// that would be responsible for this namespace.
				// This CR would live *in* the namespace `o.GetName()`.
				// We assume a default name for this CR.
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
