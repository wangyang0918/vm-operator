package controller

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	sandboxv1alpha1 "github.com/wangyang0918/vm-operator/api/v1alpha1"
)

const (
	// LabelRole is the label key identifying the role of a Pod.
	LabelRole = "sandbox.e2b.io/role"
	// LabelSandboxName is the label key carrying the owning Sandbox name.
	LabelSandboxName = "sandbox.e2b.io/sandbox-name"

	// RoleLauncher is the value of LabelRole for launcher pods.
	RoleLauncher = "launcher"

	// LauncherImage is the default image used for the launcher container.
	LauncherImage = "ghcr.io/wangyang0918/sandbox-launcher:latest"
)

// launcherPodName returns the deterministic name for the launcher Pod of a Sandbox.
func launcherPodName(sandboxName string) string {
	return fmt.Sprintf("sbx-launcher-%s", sandboxName)
}

// buildLauncherPod constructs the launcher Pod object for the given Sandbox.
func buildLauncherPod(sandbox *sandboxv1alpha1.Sandbox) *corev1.Pod {
	podName := launcherPodName(sandbox.Name)

	privileged := true
	hostPathType := corev1.HostPathCharDev

	// Map MicroVM resources to container resource requirements.
	cpuQuantity := resource.NewMilliQuantity(int64(sandbox.Spec.Resources.VCPU)*1000, resource.DecimalSI)
	memQuantity := resource.NewQuantity(int64(sandbox.Spec.Resources.MemoryMB)*1024*1024, resource.BinarySI)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: sandbox.Namespace,
			Labels: map[string]string{
				LabelRole:        RoleLauncher,
				LabelSandboxName: sandbox.Name,
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         sandboxv1alpha1.GroupVersion.String(),
					Kind:               "Sandbox",
					Name:               sandbox.Name,
					UID:                sandbox.UID,
					Controller:         boolPtr(true),
					BlockOwnerDeletion: boolPtr(true),
				},
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			NodeName:      sandbox.Spec.Scheduling.NodeName,
			NodeSelector:  sandbox.Spec.Scheduling.NodeSelector,
			Containers: []corev1.Container{
				{
					Name:  "sandbox-launcher",
					Image: LauncherImage,
					Env: buildEnvVars(sandbox),
					SecurityContext: &corev1.SecurityContext{
						Privileged: &privileged,
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    *cpuQuantity,
							corev1.ResourceMemory: *memQuantity,
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    *cpuQuantity,
							corev1.ResourceMemory: *memQuantity,
						},
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "kvm",
							MountPath: "/dev/kvm",
						},
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "kvm",
					VolumeSource: corev1.VolumeSource{
						HostPath: &corev1.HostPathVolumeSource{
							Path: "/dev/kvm",
							Type: &hostPathType,
						},
					},
				},
			},
		},
	}

	return pod
}

// buildEnvVars creates the environment variables for the launcher container.
func buildEnvVars(sandbox *sandboxv1alpha1.Sandbox) []corev1.EnvVar {
	envVars := []corev1.EnvVar{
		{Name: "SANDBOX_NAME", Value: sandbox.Name},
		{Name: "SANDBOX_NAMESPACE", Value: sandbox.Namespace},
		{Name: "TEMPLATE_ID", Value: sandbox.Spec.Template.TemplateID},
		{Name: "VCPU", Value: fmt.Sprintf("%d", sandbox.Spec.Resources.VCPU)},
		{Name: "MEMORY_MB", Value: fmt.Sprintf("%d", sandbox.Spec.Resources.MemoryMB)},
		{Name: "DISK_MB", Value: fmt.Sprintf("%d", sandbox.Spec.Resources.DiskMB)},
	}

	if sandbox.Spec.Template.BaseTemplateID != "" {
		envVars = append(envVars, corev1.EnvVar{
			Name:  "BASE_TEMPLATE_ID",
			Value: sandbox.Spec.Template.BaseTemplateID,
		})
	}

	if sandbox.Spec.Runtime.KernelVersion != "" {
		envVars = append(envVars, corev1.EnvVar{
			Name:  "KERNEL_VERSION",
			Value: sandbox.Spec.Runtime.KernelVersion,
		})
	}

	if sandbox.Spec.Runtime.FirecrackerVersion != "" {
		envVars = append(envVars, corev1.EnvVar{
			Name:  "FIRECRACKER_VERSION",
			Value: sandbox.Spec.Runtime.FirecrackerVersion,
		})
	}

	if sandbox.Spec.Resources.HugePages {
		envVars = append(envVars, corev1.EnvVar{
			Name:  "HUGE_PAGES",
			Value: "true",
		})
	}

	return envVars
}

// boolPtr returns a pointer to the given bool value.
func boolPtr(b bool) *bool {
	return &b
}
