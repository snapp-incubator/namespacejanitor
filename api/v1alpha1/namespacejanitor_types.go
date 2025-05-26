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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// NamespaceJanitorSpec defines the desired state of NamespaceJanitor
type NamespaceJanitorSpec struct {
	// AdditionalRecipients is a list of email addresses for receiving notifications
	// about lifecycle events for the namespace.
	// +optional
	AdditionalRecipients []string `json:"additionalRecipients,omitempty"`
	// TODO(Extend Deletation Threshold)
}

// NamespaceJanitorStatus defines the observed state of NamespaceJanitor
type NamespaceJanitorStatus struct {
	// LastFlagApplied is the most recent lifecycle event that was applied to the namespace.
	// +optional
	LastFlagApplied string `json:"lastFlagApplied,omitempty"`
	// LastNotificationSent records the timestamp of the last notification sent to the namespace.
	// +optional
	LastNotificationSent *metav1.Time `json:"lastNotificationSent,omitempty"`
	// Conditions represent the latest available observations of an object's current state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
//+kubebuilder:resource:scope=Namespaced,shortName=nsj;nsjanitor
// NamespaceJanitor is the Schema for the namespacejanitors API

// NamespaceJanitor is the Schema for the namespacejanitors API
type NamespaceJanitor struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NamespaceJanitorSpec   `json:"spec,omitempty"`
	Status NamespaceJanitorStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NamespaceJanitorList contains a list of NamespaceJanitor
type NamespaceJanitorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NamespaceJanitor `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NamespaceJanitor{}, &NamespaceJanitorList{})
}
