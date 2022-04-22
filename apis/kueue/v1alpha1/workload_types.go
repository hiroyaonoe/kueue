/*
Copyright 2022 The Kubernetes Authors.

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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// WorkloadSpec defines the desired state of Workload
type WorkloadSpec struct {
	// pods is a list of sets of homogeneous pods, each described by a Pod spec
	// and a count.
	//
	// +listType=map
	// +listMapKey=name
	PodSets []PodSet `json:"podSets,omitempty"`

	// queueName is the name of the queue the Workload is associated with.
	QueueName string `json:"queueName"`

	// admission holds the parameters of the admission of the workload by a ClusterQueue.
	// optionalでcqやflavorを指定できる
	// cq controllerの requestForWorkloadClusterQueue を見るかんじ、QueueNameよりも優先される？
	Admission *Admission `json:"admission,omitempty"`

	// If specified, indicates the workload's priority.
	// "system-node-critical" and "system-cluster-critical" are two special
	// keywords which indicate the highest priorities with the former being
	// the highest priority. Any other name must be defined by creating a
	// PriorityClass object with that name. If not specified, the workload
	// priority will be default or zero if there is no default.
	PriorityClassName string `json:"priorityClassName,omitempty"`

	// Priority determines the order of access to the resources managed by the
	// ClusterQueue where the workload is queued.
	// The priority value is populated from PriorityClassName.
	// The higher the value, the higher the priority.
	Priority *int32 `json:"priority,omitempty"`
}

type Admission struct {
	// clusterQueue is the name of the ClusterQueue that admitted this workload.
	ClusterQueue ClusterQueueReference `json:"clusterQueue"`

	// podSetFlavors hold the admission results for each of the .spec.podSets entries.
	// +listType=map
	// +listMapKey=name
	PodSetFlavors []PodSetFlavors `json:"podSetFlavors"`
}

type PodSetFlavors struct {
	// Name is the name of the podSet. It should match one of the names in .spec.podSets.
	// +kubebuilder:default=main
	Name string `json:"name"`

	// Flavors are the flavors assigned to the workload for each resource.
	Flavors map[corev1.ResourceName]string `json:"flavors,omitempty"`
}

type PodSet struct {
	// name is the PodSet name.
	// +kubebuilder:default=main
	Name string `json:"name"`

	// spec is the Pod spec.
	Spec corev1.PodSpec `json:"spec"`

	// count is the number of pods for the spec.
	Count int32 `json:"count"`
}

// WorkloadStatus defines the observed state of Workload
type WorkloadStatus struct {
	// conditions hold the latest available observations of the Workload
	// current state.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []WorkloadCondition `json:"conditions,omitempty"`
}

type WorkloadCondition struct {
	// type of condition could be:
	//
	// Admitted: the Workload was admitted through a ClusterQueue.
	//
	// Finished: the associated workload finished running (failed or succeeded).
	Type WorkloadConditionType `json:"type"`

	// status could be True, False or Unknown.
	Status corev1.ConditionStatus `json:"status"`

	// lastProbeTime is the last time the condition was checked.
	// +optional
	LastProbeTime metav1.Time `json:"lastProbeTime,omitempty"`

	// lastTransitionTime is the last time the condition transit from one status
	// to another.
	// +optional
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty"`

	// reason is a brief reason for the condition's last transition.
	// +optional
	Reason string `json:"reason,omitempty"`

	// message is a human readable message indicating details about last
	// transition.
	// +optional
	Message string `json:"message,omitempty"`
}

type WorkloadConditionType string

const (
	// WorkloadAdmitted means that the Workload was admitted by a ClusterQueue.
	WorkloadAdmitted WorkloadConditionType = "Admitted"

	// WorkloadFinished means that the workload associated to the
	// ResourceClaim finished running (failed or succeeded).
	WorkloadFinished WorkloadConditionType = "Finished"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Queue",JSONPath=".spec.queueName",type=string,description="Name of the queue this workload was submitted to"
// +kubebuilder:printcolumn:name="Admitted by",JSONPath=".spec.admission.clusterQueue",type=string,description="Name of the ClusterQueue that admitted this workload"
// +kubebuilder:printcolumn:name="Age",JSONPath=".metadata.creationTimestamp",type=date,description="Time this workload was created"

// Workload is the Schema for the workloads API
type Workload struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WorkloadSpec   `json:"spec,omitempty"`
	Status WorkloadStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// WorkloadList contains a list of ResourceClaim
type WorkloadList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Workload `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Workload{}, &WorkloadList{})
}
