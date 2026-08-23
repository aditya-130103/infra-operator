/*
Copyright 2026.

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
	"k8s.io/apimachinery/pkg/runtime"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

type AWSCredsRefSpec struct {
	// +kubebuilder:validation:Required
	Name string `json:"name"`
	// +kubebuilder:validation:Required
	Namespace string `json:"namespace"`
}

// VPCSpec defines the desired state of VPC
type VPCSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	// The following markers will use OpenAPI v3 schema to validate the value
	// More info: https://book.kubebuilder.io/reference/markers/crd-validation.html

	// +kubebuilder:validation:Required
	CIDRBlock string `json:"cidrBlock"`
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=us-east-1;us-east-2;us-west-1;us-west-2;ca-central-1;ca-west-1;eu-west-1;eu-west-2;eu-west-3;eu-central-1;eu-central-2;eu-north-1;eu-south-1;eu-south-2;ap-south-1;ap-south-2;ap-northeast-1;ap-northeast-2;ap-northeast-3;ap-southeast-1;ap-southeast-2;ap-southeast-3;ap-southeast-4;ap-southeast-5;ap-east-1;sa-east-1;me-south-1;me-central-1;af-south-1;il-central-1;mx-central-1
	Region string `json:"region"`
	// +kubebuilder:validation:Required
	AWSCredsRef AWSCredsRefSpec `json:"awsCredsRef"`
}

// VPCStatus defines the observed state of VPC.
type VPCStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	VpcID string `json:"vpcId,omitempty"`
	// +kubebuilder:validation:Enum=Pending;Creating;Available;Failed;Deleting
	Phase string `json:"phase,omitempty"`
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="VpcID",type="string",JSONPath=".status.vpcId"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// VPC is the Schema for the vpcs API
type VPC struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of VPC
	// +required
	Spec VPCSpec `json:"spec"`

	// status defines the observed state of VPC
	// +optional
	Status VPCStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// VPCList contains a list of VPC
type VPCList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []VPC `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &VPC{}, &VPCList{})
		return nil
	})
}
