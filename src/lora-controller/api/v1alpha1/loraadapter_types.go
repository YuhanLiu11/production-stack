package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AdapterSource defines the source of the LoRA adapter
type AdapterSource struct {
	// Type is the type of adapter source (e.g., "local", "s3", "http")
	// +kubebuilder:validation:Required
	Type string `json:"type"`

	// Repository is the repository where the adapter is stored
	// +kubebuilder:validation:Required
	Repository string `json:"repository"`

	// AdapterPath is the path to the LoRA adapter weights.
	// For local sources: required, specifies the path to the adapter
	// For remote sources: optional, will be updated by the controller with the download path
	// +optional
	AdapterPath string `json:"adapterPath,omitempty"`

	// AdapterName is the name of the adapter to apply
	// +kubebuilder:validation:Required
	AdapterName string `json:"adapterName"`

	// Pattern is a regex pattern to filter adapters (for s3/cos)
	// +optional
	Pattern string `json:"pattern,omitempty"`

	// MaxAdapters is the maximum number of adapters to load
	// +optional
	MaxAdapters *int32 `json:"maxAdapters,omitempty"`

	// CredentialsSecretRef references a secret containing storage credentials
	// +optional
	CredentialsSecretRef *corev1.LocalObjectReference `json:"credentialsSecretRef,omitempty"`
}

// DeploymentConfig defines how the adapter should be deployed
type DeploymentConfig struct {
	// Replicas is the number of replicas that should load this adapter
	// +kubebuilder:validation:Minimum=0
	// +optional
	Replicas int32 `json:"replicas,omitempty"`

	// Algorithm specifies which placement algorithm to use
	// +kubebuilder:validation:Enum=default;ordered;equalized
	// +kubebuilder:default=default
	Algorithm string `json:"algorithm"`
}

// LoraAdapterSpec defines the desired state of LoraAdapter
type LoraAdapterSpec struct {
	// BaseModel is the name of the base model this adapter is for
	// +kubebuilder:validation:Required
	BaseModel string `json:"baseModel"`

	// AdapterSource defines where to get the LoRA adapter from
	// +kubebuilder:validation:Required
	AdapterSource AdapterSource `json:"adapterSource"`

	// DeploymentConfig defines how the adapter should be deployed
	// +optional
	DeploymentConfig DeploymentConfig `json:"deploymentConfig,omitempty"`
}

// PodAssignment represents a pod that has been assigned to load this adapter
type PodAssignment struct {
	// Pod represents the pod information
	Pod corev1.ObjectReference `json:"pod"`

	// Status represents the current status of the assignment
	// Can be "Ready", "Failed", "Pending"
	Status string `json:"status"`
}

// LoadedAdapter represents an adapter that has been loaded into a pod
type LoadedAdapter struct {
	// Name is the name of the adapter
	Name string `json:"name"`

	// Path is the path where the adapter is loaded
	Path string `json:"path"`

	// Status represents the current status of the loaded adapter
	Status string `json:"status"`

	// LoadTime is when the adapter was loaded
	LoadTime *metav1.Time `json:"loadTime,omitempty"`

	// PodAssignments represents the pods this adapter has been assigned to
	PodAssignments []PodAssignment `json:"podAssignments"`
}

// LoraAdapterStatus defines the observed state of LoraAdapter
type LoraAdapterStatus struct {
	// Phase represents the current phase of the adapter deployment
	// +optional
	Phase string `json:"phase,omitempty"`

	// Message provides additional information about the current phase
	// +optional
	Message string `json:"message,omitempty"`

	// LoadedAdapters tracks the loading status of adapters and their pod assignments
	// +optional
	LoadedAdapters []LoadedAdapter `json:"loadedAdapters,omitempty"`

	// Conditions represent the latest available observations of the adapter's state
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
//+kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// LoraAdapter is the Schema for the loraadapters API
type LoraAdapter struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   LoraAdapterSpec   `json:"spec,omitempty"`
	Status LoraAdapterStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// LoraAdapterList contains a list of LoraAdapter
type LoraAdapterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []LoraAdapter `json:"items"`
}

func init() {
	SchemeBuilder.Register(&LoraAdapter{}, &LoraAdapterList{})
}
