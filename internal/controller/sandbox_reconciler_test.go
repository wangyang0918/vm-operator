package controller

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	sandboxv1alpha1 "github.com/wangyang0918/vm-operator/api/v1alpha1"
)

// newScheme creates a runtime.Scheme with all required types registered.
func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatalf("failed to add clientgo scheme: %v", err)
	}
	if err := sandboxv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("failed to add sandbox scheme: %v", err)
	}
	return s
}

// newReconciler creates a SandboxReconciler backed by a fake client seeded with objs.
func newReconciler(t *testing.T, objs ...runtime.Object) *SandboxReconciler {
	t.Helper()
	s := newScheme(t)
	builder := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&sandboxv1alpha1.Sandbox{})
	for _, o := range objs {
		builder = builder.WithRuntimeObjects(o)
	}
	return &SandboxReconciler{
		Client: builder.Build(),
		Scheme: s,
	}
}

// newSandbox returns a minimal Sandbox CR suitable for tests.
func newSandbox(name, namespace string) *sandboxv1alpha1.Sandbox {
	return &sandboxv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Namespace:  namespace,
			Generation: 1,
		},
		Spec: sandboxv1alpha1.SandboxSpec{
			Template: sandboxv1alpha1.TemplateSpec{TemplateID: "tpl-001"},
			Resources: sandboxv1alpha1.ResourcesSpec{
				VCPU:     2,
				MemoryMB: 512,
				DiskMB:   2048,
			},
			Lifecycle: sandboxv1alpha1.LifecycleSpec{TimeoutSeconds: 300},
		},
	}
}

// reconcileN calls Reconcile n times and returns the last result.
func reconcileN(t *testing.T, r *SandboxReconciler, req ctrl.Request, n int) ctrl.Result {
	t.Helper()
	var result ctrl.Result
	var err error
	for i := 0; i < n; i++ {
		result, err = r.Reconcile(context.Background(), req)
		if err != nil {
			t.Fatalf("Reconcile[%d] returned error: %v", i, err)
		}
	}
	return result
}

// getSandbox fetches the current state of a Sandbox from the fake client.
func getSandbox(t *testing.T, r *SandboxReconciler, name, namespace string) *sandboxv1alpha1.Sandbox {
	t.Helper()
	sbx := &sandboxv1alpha1.Sandbox{}
	if err := r.Get(context.Background(), types.NamespacedName{Name: name, Namespace: namespace}, sbx); err != nil {
		t.Fatalf("failed to get sandbox: %v", err)
	}
	return sbx
}

// getPod fetches a Pod from the fake client.
func getPod(t *testing.T, r *SandboxReconciler, name, namespace string) (*corev1.Pod, bool) {
	t.Helper()
	pod := &corev1.Pod{}
	err := r.Get(context.Background(), types.NamespacedName{Name: name, Namespace: namespace}, pod)
	if err != nil {
		return nil, false
	}
	return pod, true
}

// --- Tests ---

// TestNewCR_FinalizerAndPending verifies that a brand-new Sandbox CR gets the
// finalizer added and transitions to Pending.
func TestNewCR_FinalizerAndPending(t *testing.T) {
	sandbox := newSandbox("test-sbx", "default")
	r := newReconciler(t, sandbox)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-sbx", Namespace: "default"}}

	// First reconcile: adds finalizer + transitions to Pending.
	reconcileN(t, r, req, 1)

	got := getSandbox(t, r, "test-sbx", "default")

	hasFinalizer := false
	for _, f := range got.Finalizers {
		if f == SandboxFinalizer {
			hasFinalizer = true
		}
	}
	if !hasFinalizer {
		t.Errorf("expected finalizer %q to be present", SandboxFinalizer)
	}
	if got.Status.Phase != sandboxv1alpha1.SandboxPhasePending {
		t.Errorf("expected phase Pending, got %q", got.Status.Phase)
	}
}

// TestPending_CreatesPodAndScheduling verifies that reconciling a Pending Sandbox
// creates the launcher Pod and advances the phase to Scheduling.
func TestPending_CreatesPodAndScheduling(t *testing.T) {
	sandbox := newSandbox("test-sbx", "default")
	sandbox.Finalizers = []string{SandboxFinalizer}
	sandbox.Status.Phase = sandboxv1alpha1.SandboxPhasePending
	r := newReconciler(t, sandbox)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-sbx", Namespace: "default"}}

	result, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should requeue after requeueScheduling.
	if result.RequeueAfter != requeueScheduling {
		t.Errorf("expected RequeueAfter=%v, got %v", requeueScheduling, result.RequeueAfter)
	}

	got := getSandbox(t, r, "test-sbx", "default")
	if got.Status.Phase != sandboxv1alpha1.SandboxPhaseScheduling {
		t.Errorf("expected phase Scheduling, got %q", got.Status.Phase)
	}
	if got.Status.PodName != launcherPodName("test-sbx") {
		t.Errorf("expected PodName %q, got %q", launcherPodName("test-sbx"), got.Status.PodName)
	}

	podName := launcherPodName("test-sbx")
	if _, ok := getPod(t, r, podName, "default"); !ok {
		t.Errorf("expected launcher Pod %q to exist", podName)
	}
}

// TestScheduling_NodeAssigned_Initializing verifies that once the launcher Pod
// has a NodeName, the Sandbox moves to Initializing.
func TestScheduling_NodeAssigned_Initializing(t *testing.T) {
	sandbox := newSandbox("test-sbx", "default")
	sandbox.Finalizers = []string{SandboxFinalizer}
	sandbox.Status.Phase = sandboxv1alpha1.SandboxPhaseScheduling
	sandbox.Status.PodName = launcherPodName("test-sbx")

	pod := buildLauncherPod(sandbox)
	pod.Spec.NodeName = "node-1" // Simulate scheduler assignment.

	r := newReconciler(t, sandbox, pod)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-sbx", Namespace: "default"}}

	result, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter != requeueInitializing {
		t.Errorf("expected RequeueAfter=%v, got %v", requeueInitializing, result.RequeueAfter)
	}

	got := getSandbox(t, r, "test-sbx", "default")
	if got.Status.Phase != sandboxv1alpha1.SandboxPhaseInitializing {
		t.Errorf("expected phase Initializing, got %q", got.Status.Phase)
	}
	if got.Status.NodeName != "node-1" {
		t.Errorf("expected NodeName node-1, got %q", got.Status.NodeName)
	}
}

// TestScheduling_Unschedulable_Failed verifies that an Unschedulable Pod moves
// the Sandbox to Failed.
func TestScheduling_Unschedulable_Failed(t *testing.T) {
	sandbox := newSandbox("test-sbx", "default")
	sandbox.Finalizers = []string{SandboxFinalizer}
	sandbox.Status.Phase = sandboxv1alpha1.SandboxPhaseScheduling
	sandbox.Status.PodName = launcherPodName("test-sbx")

	pod := buildLauncherPod(sandbox)
	pod.Status.Conditions = []corev1.PodCondition{
		{
			Type:    corev1.PodScheduled,
			Status:  corev1.ConditionFalse,
			Reason:  corev1.PodReasonUnschedulable,
			Message: "0/1 nodes available",
		},
	}

	r := newReconciler(t, sandbox, pod)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-sbx", Namespace: "default"}}

	_, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := getSandbox(t, r, "test-sbx", "default")
	if got.Status.Phase != sandboxv1alpha1.SandboxPhaseFailed {
		t.Errorf("expected phase Failed, got %q", got.Status.Phase)
	}
}

// TestRunning_Timeout_Killing verifies that an expired timeout moves the Sandbox to Killing
// and removes the finalizer after the Pod is deleted.
func TestRunning_Timeout_Killing(t *testing.T) {
	sandbox := newSandbox("test-sbx", "default")
	sandbox.Finalizers = []string{SandboxFinalizer}
	sandbox.Spec.Lifecycle.TimeoutSeconds = 1 // 1-second timeout so it's already expired.

	pastTime := metav1.NewTime(time.Now().Add(-10 * time.Second))
	sandbox.Status.Phase = sandboxv1alpha1.SandboxPhaseRunning
	sandbox.Status.StartTime = &pastTime
	sandbox.Status.PodName = launcherPodName("test-sbx")

	pod := buildLauncherPod(sandbox)
	pod.Status.Phase = corev1.PodRunning

	r := newReconciler(t, sandbox, pod)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-sbx", Namespace: "default"}}

	_, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := getSandbox(t, r, "test-sbx", "default")
	if got.Status.Phase != sandboxv1alpha1.SandboxPhaseKilling {
		t.Errorf("expected phase Killing, got %q", got.Status.Phase)
	}

	// Pod should have been deleted.
	if _, ok := getPod(t, r, launcherPodName("test-sbx"), "default"); ok {
		t.Errorf("expected launcher Pod to be deleted")
	}

	// Finalizer should have been removed.
	for _, f := range got.Finalizers {
		if f == SandboxFinalizer {
			t.Errorf("expected finalizer to be removed")
		}
	}
}

// TestInitializing_PodRunning_Running verifies that a Running pod during
// Initializing moves the Sandbox to Running and records StartTime/EndTime.
func TestInitializing_PodRunning_Running(t *testing.T) {
	sandbox := newSandbox("test-sbx", "default")
	sandbox.Finalizers = []string{SandboxFinalizer}
	sandbox.Status.Phase = sandboxv1alpha1.SandboxPhaseInitializing
	sandbox.Status.PodName = launcherPodName("test-sbx")

	pod := buildLauncherPod(sandbox)
	pod.Status.Phase = corev1.PodRunning

	r := newReconciler(t, sandbox, pod)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-sbx", Namespace: "default"}}

	result, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter != requeueRunning {
		t.Errorf("expected RequeueAfter=%v, got %v", requeueRunning, result.RequeueAfter)
	}

	got := getSandbox(t, r, "test-sbx", "default")
	if got.Status.Phase != sandboxv1alpha1.SandboxPhaseRunning {
		t.Errorf("expected phase Running, got %q", got.Status.Phase)
	}
	if got.Status.StartTime == nil {
		t.Error("expected StartTime to be set")
	}
	if got.Status.EndTime == nil {
		t.Error("expected EndTime to be set (timeout configured)")
	}
}

// TestInitializing_PodFailed_Failed verifies that a Failed pod during
// Initializing moves the Sandbox to Failed.
func TestInitializing_PodFailed_Failed(t *testing.T) {
	sandbox := newSandbox("test-sbx", "default")
	sandbox.Finalizers = []string{SandboxFinalizer}
	sandbox.Status.Phase = sandboxv1alpha1.SandboxPhaseInitializing
	sandbox.Status.PodName = launcherPodName("test-sbx")

	pod := buildLauncherPod(sandbox)
	pod.Status.Phase = corev1.PodFailed

	r := newReconciler(t, sandbox, pod)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-sbx", Namespace: "default"}}

	_, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := getSandbox(t, r, "test-sbx", "default")
	if got.Status.Phase != sandboxv1alpha1.SandboxPhaseFailed {
		t.Errorf("expected phase Failed, got %q", got.Status.Phase)
	}
}

// TestInitializing_PodNotFound_RevertsPending verifies that a missing pod
// during Initializing reverts the Sandbox to Pending.
func TestInitializing_PodNotFound_RevertsPending(t *testing.T) {
	sandbox := newSandbox("test-sbx", "default")
	sandbox.Finalizers = []string{SandboxFinalizer}
	sandbox.Status.Phase = sandboxv1alpha1.SandboxPhaseInitializing
	sandbox.Status.PodName = launcherPodName("test-sbx")
	// No pod added to the fake client.

	r := newReconciler(t, sandbox)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-sbx", Namespace: "default"}}

	_, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := getSandbox(t, r, "test-sbx", "default")
	if got.Status.Phase != sandboxv1alpha1.SandboxPhasePending {
		t.Errorf("expected phase Pending, got %q", got.Status.Phase)
	}
}

// TestInitializing_PodPending_Requeues verifies that a still-pending pod
// during Initializing causes a requeue without phase change.
func TestInitializing_PodPending_Requeues(t *testing.T) {
	sandbox := newSandbox("test-sbx", "default")
	sandbox.Finalizers = []string{SandboxFinalizer}
	sandbox.Status.Phase = sandboxv1alpha1.SandboxPhaseInitializing
	sandbox.Status.PodName = launcherPodName("test-sbx")

	pod := buildLauncherPod(sandbox)
	pod.Status.Phase = corev1.PodPending

	r := newReconciler(t, sandbox, pod)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-sbx", Namespace: "default"}}

	result, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter != requeueInitializing {
		t.Errorf("expected RequeueAfter=%v, got %v", requeueInitializing, result.RequeueAfter)
	}

	got := getSandbox(t, r, "test-sbx", "default")
	if got.Status.Phase != sandboxv1alpha1.SandboxPhaseInitializing {
		t.Errorf("expected phase Initializing, got %q", got.Status.Phase)
	}
}

// TestRunning_PodMissing_Failed verifies that a missing launcher Pod during
// Running moves the Sandbox to Failed.
func TestRunning_PodMissing_Failed(t *testing.T) {
	pastTime := metav1.NewTime(time.Now().Add(-10 * time.Second))
	sandbox := newSandbox("test-sbx", "default")
	sandbox.Finalizers = []string{SandboxFinalizer}
	sandbox.Status.Phase = sandboxv1alpha1.SandboxPhaseRunning
	sandbox.Status.StartTime = &pastTime
	sandbox.Status.PodName = launcherPodName("test-sbx")
	// No pod in the fake client.

	r := newReconciler(t, sandbox)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-sbx", Namespace: "default"}}

	_, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := getSandbox(t, r, "test-sbx", "default")
	if got.Status.Phase != sandboxv1alpha1.SandboxPhaseFailed {
		t.Errorf("expected phase Failed, got %q", got.Status.Phase)
	}
}

// TestRunning_PodTerminated_Killing verifies that a terminated (Succeeded or
// Failed) launcher Pod during Running moves the Sandbox to Killing.
func TestRunning_PodTerminated_Killing(t *testing.T) {
	for _, podPhase := range []corev1.PodPhase{corev1.PodFailed, corev1.PodSucceeded} {
		t.Run(string(podPhase), func(t *testing.T) {
			pastTime := metav1.NewTime(time.Now().Add(-1 * time.Second))
			sandbox := newSandbox("test-sbx", "default")
			sandbox.Finalizers = []string{SandboxFinalizer}
			// Use a long timeout so we don't hit the timeout path.
			sandbox.Spec.Lifecycle.TimeoutSeconds = 3600
			sandbox.Status.Phase = sandboxv1alpha1.SandboxPhaseRunning
			sandbox.Status.StartTime = &pastTime
			sandbox.Status.PodName = launcherPodName("test-sbx")

			pod := buildLauncherPod(sandbox)
			pod.Status.Phase = podPhase

			r := newReconciler(t, sandbox, pod)
			req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-sbx", Namespace: "default"}}

			_, err := r.Reconcile(context.Background(), req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			got := getSandbox(t, r, "test-sbx", "default")
			if got.Status.Phase != sandboxv1alpha1.SandboxPhaseKilling {
				t.Errorf("expected phase Killing, got %q", got.Status.Phase)
			}
		})
	}
}

// TestRunning_NoTimeout_Requeues verifies that a Running sandbox without an
// expired timeout requeues after requeueRunning.
func TestRunning_NoTimeout_Requeues(t *testing.T) {
	startTime := metav1.Now()
	sandbox := newSandbox("test-sbx", "default")
	sandbox.Finalizers = []string{SandboxFinalizer}
	sandbox.Spec.Lifecycle.TimeoutSeconds = 3600
	sandbox.Status.Phase = sandboxv1alpha1.SandboxPhaseRunning
	sandbox.Status.StartTime = &startTime
	sandbox.Status.PodName = launcherPodName("test-sbx")

	pod := buildLauncherPod(sandbox)
	pod.Status.Phase = corev1.PodRunning

	r := newReconciler(t, sandbox, pod)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-sbx", Namespace: "default"}}

	result, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter != requeueRunning {
		t.Errorf("expected RequeueAfter=%v, got %v", requeueRunning, result.RequeueAfter)
	}
}

// TestBuildEnvVars_OptionalFields verifies that optional fields are included
// in the launcher Pod environment when set.
func TestBuildEnvVars_OptionalFields(t *testing.T) {
	sandbox := newSandbox("test-sbx", "default")
	sandbox.Spec.Template.BaseTemplateID = "base-tpl-001"
	sandbox.Spec.Runtime.KernelVersion = "5.10.186"
	sandbox.Spec.Runtime.FirecrackerVersion = "1.7.0"
	sandbox.Spec.Resources.HugePages = true

	envVars := buildEnvVars(sandbox)

	want := map[string]string{
		"SANDBOX_NAME":        "test-sbx",
		"SANDBOX_NAMESPACE":   "default",
		"TEMPLATE_ID":         "tpl-001",
		"BASE_TEMPLATE_ID":    "base-tpl-001",
		"KERNEL_VERSION":      "5.10.186",
		"FIRECRACKER_VERSION": "1.7.0",
		"HUGE_PAGES":          "true",
	}

	got := make(map[string]string, len(envVars))
	for _, e := range envVars {
		got[e.Name] = e.Value
	}

	for k, v := range want {
		if got[k] != v {
			t.Errorf("env %q: expected %q, got %q", k, v, got[k])
		}
	}
}

// TestDeletionTimestamp_KillingAndFinalizerRemoved verifies that setting a
// DeletionTimestamp moves the Sandbox to Killing and removes the finalizer.
func TestDeletionTimestamp_KillingAndFinalizerRemoved(t *testing.T) {
	now := metav1.Now()
	sandbox := newSandbox("test-sbx", "default")
	sandbox.Finalizers = []string{SandboxFinalizer}
	sandbox.DeletionTimestamp = &now
	sandbox.Status.Phase = sandboxv1alpha1.SandboxPhaseRunning
	sandbox.Status.PodName = launcherPodName("test-sbx")

	pod := buildLauncherPod(sandbox)

	r := newReconciler(t, sandbox, pod)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-sbx", Namespace: "default"}}

	_, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Pod should be deleted.
	if _, ok := getPod(t, r, launcherPodName("test-sbx"), "default"); ok {
		t.Errorf("expected launcher Pod to be deleted")
	}

	// The sandbox may have been garbage-collected once all finalizers were removed.
	// Check the final state: either deleted or in Killing phase with no finalizer.
	got := &sandboxv1alpha1.Sandbox{}
	err = r.Get(context.Background(), types.NamespacedName{Name: "test-sbx", Namespace: "default"}, got)
	if err != nil {
		// Sandbox was garbage collected — this is the expected happy path.
		return
	}
	if got.Status.Phase != sandboxv1alpha1.SandboxPhaseKilling {
		t.Errorf("expected phase Killing, got %q", got.Status.Phase)
	}
	for _, f := range got.Finalizers {
		if f == SandboxFinalizer {
			t.Errorf("expected finalizer to be removed")
		}
	}
}
