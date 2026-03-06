package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SandboxPhase defines the lifecycle phase of a Sandbox.
type SandboxPhase string

const (
	// SandboxPhasePending means the Sandbox has been accepted but not yet scheduled.
	SandboxPhasePending SandboxPhase = "Pending"
	// SandboxPhaseScheduling means the launcher Pod has been created and is being scheduled.
	SandboxPhaseScheduling SandboxPhase = "Scheduling"
	// SandboxPhaseInitializing means the Pod is scheduled and the MicroVM is starting.
	SandboxPhaseInitializing SandboxPhase = "Initializing"
	// SandboxPhaseRunning means the MicroVM is running.
	SandboxPhaseRunning SandboxPhase = "Running"
	// SandboxPhasePausing means the Sandbox is saving a VM snapshot and stopping the launcher Pod.
	SandboxPhasePausing SandboxPhase = "Pausing"
	// SandboxPhasePaused means the VM snapshot has been saved and the launcher Pod has been deleted.
	SandboxPhasePaused SandboxPhase = "Paused"
	// SandboxPhaseResuming means a new launcher Pod is being created from the saved snapshot.
	SandboxPhaseResuming SandboxPhase = "Resuming"
	// SandboxPhaseKilling means the Sandbox is being terminated.
	SandboxPhaseKilling SandboxPhase = "Killing"
	// SandboxPhaseFailed means the Sandbox has encountered an unrecoverable error.
	SandboxPhaseFailed SandboxPhase = "Failed"
)

// Condition type constants for Sandbox status conditions.
const (
	// ConditionTypeSnapshotReady indicates that a VM snapshot has been successfully taken.
	ConditionTypeSnapshotReady = "SnapshotReady"
)

// TemplateSpec defines the template information for a Sandbox.
type TemplateSpec struct {
	// TemplateID is the required template identifier.
	// +kubebuilder:validation:Required
	TemplateID string `json:"templateID"`

	// BaseTemplateID is the base template identifier.
	// +optional
	BaseTemplateID string `json:"baseTemplateID,omitempty"`
}

// ResourcesSpec defines the compute resources for the MicroVM.
type ResourcesSpec struct {
	// VCPU is the number of virtual CPUs.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=64
	// +kubebuilder:default=2
	VCPU int32 `json:"vcpu"`

	// MemoryMB is the memory size in megabytes.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=128
	// +kubebuilder:validation:Maximum=65536
	// +kubebuilder:default=512
	MemoryMB int32 `json:"memoryMB"`

	// DiskMB is the disk size in megabytes.
	// +kubebuilder:default=2048
	// +optional
	DiskMB int32 `json:"diskMB,omitempty"`

	// HugePages enables huge page memory support.
	// +kubebuilder:default=false
	// +optional
	HugePages bool `json:"hugePages,omitempty"`
}

// RuntimeSpec defines the runtime configuration for the MicroVM.
type RuntimeSpec struct {
	// KernelVersion specifies the kernel version to use.
	// +optional
	KernelVersion string `json:"kernelVersion,omitempty"`

	// FirecrackerVersion specifies the Firecracker binary version to use.
	// +optional
	FirecrackerVersion string `json:"firecrackerVersion,omitempty"`
}

// LifecycleSpec defines lifecycle policies for a Sandbox.
type LifecycleSpec struct {
	// TimeoutSeconds is the maximum duration the sandbox may run before being killed.
	// +kubebuilder:default=300
	// +optional
	TimeoutSeconds int64 `json:"timeoutSeconds,omitempty"`
}

// SchedulingSpec defines scheduling constraints for the launcher Pod.
type SchedulingSpec struct {
	// NodeSelector is a map of key-value pairs for node selection.
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// NodeName specifies the exact node on which to run the launcher Pod.
	// +optional
	NodeName string `json:"nodeName,omitempty"`
}

// SandboxSpec defines the desired state of Sandbox.
type SandboxSpec struct {
	// Template contains the template information.
	// +optional
	Template TemplateSpec `json:"template,omitempty"`

	// Resources defines the compute resources for the MicroVM.
	// +kubebuilder:validation:Required
	Resources ResourcesSpec `json:"resources"`

	// Runtime defines optional runtime configuration.
	// +optional
	Runtime RuntimeSpec `json:"runtime,omitempty"`

	// Lifecycle defines lifecycle policies.
	// +optional
	Lifecycle LifecycleSpec `json:"lifecycle,omitempty"`

	// Scheduling defines scheduling constraints.
	// +optional
	Scheduling SchedulingSpec `json:"scheduling,omitempty"`

	// SandboxMetadata is user-defined metadata attached to the sandbox.
	// +optional
	SandboxMetadata map[string]string `json:"sandboxMetadata,omitempty"`

	// Paused indicates whether the sandbox should be paused.
	// +optional
	Paused bool `json:"paused,omitempty"`
}

// SandboxStatus defines the observed state of Sandbox.
type SandboxStatus struct {
	// Phase is the current lifecycle phase of the Sandbox.
	// +optional
	Phase SandboxPhase `json:"phase,omitempty"`

	// NodeName is the name of the node where the sandbox is running.
	// +optional
	NodeName string `json:"nodeName,omitempty"`

	// PodName is the name of the launcher Pod.
	// +optional
	PodName string `json:"podName,omitempty"`

	// StartTime is the time when the sandbox became Running.
	// +optional
	StartTime *metav1.Time `json:"startTime,omitempty"`

	// EndTime is the projected end time based on timeout.
	// +optional
	EndTime *metav1.Time `json:"endTime,omitempty"`

	// SnapshotID is the identifier of the VM snapshot taken when pausing.
	// +optional
	SnapshotID string `json:"snapshotId,omitempty"`

	// PausedAt is the time when the sandbox was paused.
	// +optional
	PausedAt *metav1.Time `json:"pausedAt,omitempty"`

	// Conditions contains the current service state of the Sandbox.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ObservedGeneration is the most recent generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=sbx
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Node",type=string,JSONPath=`.status.nodeName`
// +kubebuilder:printcolumn:name="Pod",type=string,JSONPath=`.status.podName`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Sandbox is the Schema for the sandboxes API.
type Sandbox struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SandboxSpec   `json:"spec,omitempty"`
	Status SandboxStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SandboxList contains a list of Sandbox.
type SandboxList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Sandbox `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Sandbox{}, &SandboxList{})
}
