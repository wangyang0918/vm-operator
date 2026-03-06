package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	sandboxv1alpha1 "github.com/wangyang0918/vm-operator/api/v1alpha1"
)

const (
	// SandboxFinalizer is the finalizer added to Sandbox resources.
	SandboxFinalizer = "sandbox.e2b.io/protection"

	// Requeue intervals for each phase.
	requeueScheduling    = 5 * time.Second
	requeueInitializing  = 3 * time.Second
	requeueRunning       = 30 * time.Second
)

// SandboxReconciler reconciles a Sandbox object.
// +kubebuilder:rbac:groups=sandbox.e2b.io,resources=sandboxes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=sandbox.e2b.io,resources=sandboxes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sandbox.e2b.io,resources=sandboxes/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
type SandboxReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// SetupWithManager registers the controller with the given manager.
func (r *SandboxReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&sandboxv1alpha1.Sandbox{}).
		Owns(&corev1.Pod{}).
		Complete(r)
}

// Reconcile is the main reconciliation loop entry point.
func (r *SandboxReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	sandbox := &sandboxv1alpha1.Sandbox{}
	if err := r.Get(ctx, req.NamespacedName, sandbox); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Handle deletion.
	if !sandbox.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, sandbox)
	}

	// Ensure finalizer is present.
	if !controllerutil.ContainsFinalizer(sandbox, SandboxFinalizer) {
		controllerutil.AddFinalizer(sandbox, SandboxFinalizer)
		if err := r.Update(ctx, sandbox); err != nil {
			return ctrl.Result{}, err
		}
		// Set phase to Pending after adding finalizer.
		return r.setPhasePending(ctx, sandbox)
	}

	// Route to the appropriate phase handler.
	switch sandbox.Status.Phase {
	case "", sandboxv1alpha1.SandboxPhasePending:
		return r.reconcilePending(ctx, sandbox)
	case sandboxv1alpha1.SandboxPhaseScheduling:
		return r.reconcileScheduling(ctx, sandbox)
	case sandboxv1alpha1.SandboxPhaseInitializing:
		return r.reconcileInitializing(ctx, sandbox)
	case sandboxv1alpha1.SandboxPhaseRunning:
		return r.reconcileRunning(ctx, sandbox)
	case sandboxv1alpha1.SandboxPhaseKilling:
		return r.reconcileKilling(ctx, sandbox)
	case sandboxv1alpha1.SandboxPhaseFailed:
		logger.Info("Sandbox is in Failed phase, no further action")
		return ctrl.Result{}, nil
	default:
		logger.Info("Unknown phase, resetting to Pending", "phase", sandbox.Status.Phase)
		return r.setPhasePending(ctx, sandbox)
	}
}

// setPhasePending transitions the sandbox to Pending phase.
func (r *SandboxReconciler) setPhasePending(ctx context.Context, sandbox *sandboxv1alpha1.Sandbox) (ctrl.Result, error) {
	sandbox.Status.Phase = sandboxv1alpha1.SandboxPhasePending
	sandbox.Status.ObservedGeneration = sandbox.Generation
	if err := r.Status().Update(ctx, sandbox); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// reconcilePending handles the Pending phase: create the launcher Pod.
func (r *SandboxReconciler) reconcilePending(ctx context.Context, sandbox *sandboxv1alpha1.Sandbox) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	pod := buildLauncherPod(sandbox)

	existing := &corev1.Pod{}
	err := r.Get(ctx, types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace}, existing)
	if apierrors.IsNotFound(err) {
		logger.Info("Creating launcher Pod", "pod", pod.Name)
		if err := r.Create(ctx, pod); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to create launcher pod: %w", err)
		}
	} else if err != nil {
		return ctrl.Result{}, err
	}

	sandbox.Status.Phase = sandboxv1alpha1.SandboxPhaseScheduling
	sandbox.Status.PodName = pod.Name
	sandbox.Status.ObservedGeneration = sandbox.Generation
	if err := r.Status().Update(ctx, sandbox); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: requeueScheduling}, nil
}

// reconcileScheduling handles the Scheduling phase: wait for Pod to be assigned a node.
func (r *SandboxReconciler) reconcileScheduling(ctx context.Context, sandbox *sandboxv1alpha1.Sandbox) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	pod := &corev1.Pod{}
	podName := launcherPodName(sandbox.Name)
	if err := r.Get(ctx, types.NamespacedName{Name: podName, Namespace: sandbox.Namespace}, pod); err != nil {
		if apierrors.IsNotFound(err) {
			// Pod disappeared; recreate by going back to Pending.
			logger.Info("Launcher Pod not found, reverting to Pending")
			return r.setPhasePending(ctx, sandbox)
		}
		return ctrl.Result{}, err
	}

	// Check for Unschedulable condition.
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse &&
			cond.Reason == corev1.PodReasonUnschedulable {
			logger.Info("Launcher Pod is Unschedulable, moving to Failed")
			return r.setPhase(ctx, sandbox, sandboxv1alpha1.SandboxPhaseFailed,
				"Unschedulable", cond.Message)
		}
	}

	// Pod has been assigned to a node.
	if pod.Spec.NodeName != "" {
		logger.Info("Launcher Pod scheduled", "node", pod.Spec.NodeName)
		sandbox.Status.NodeName = pod.Spec.NodeName
		sandbox.Status.Phase = sandboxv1alpha1.SandboxPhaseInitializing
		sandbox.Status.ObservedGeneration = sandbox.Generation
		if err := r.Status().Update(ctx, sandbox); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: requeueInitializing}, nil
	}

	return ctrl.Result{RequeueAfter: requeueScheduling}, nil
}

// reconcileInitializing handles the Initializing phase: wait for Pod to reach Running state.
func (r *SandboxReconciler) reconcileInitializing(ctx context.Context, sandbox *sandboxv1alpha1.Sandbox) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	pod := &corev1.Pod{}
	podName := launcherPodName(sandbox.Name)
	if err := r.Get(ctx, types.NamespacedName{Name: podName, Namespace: sandbox.Namespace}, pod); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Launcher Pod not found during Initializing, reverting to Pending")
			return r.setPhasePending(ctx, sandbox)
		}
		return ctrl.Result{}, err
	}

	if pod.Status.Phase == corev1.PodRunning {
		now := metav1.Now()
		sandbox.Status.Phase = sandboxv1alpha1.SandboxPhaseRunning
		sandbox.Status.StartTime = &now
		if sandbox.Spec.Lifecycle.TimeoutSeconds > 0 {
			endTime := metav1.NewTime(now.Add(time.Duration(sandbox.Spec.Lifecycle.TimeoutSeconds) * time.Second))
			sandbox.Status.EndTime = &endTime
		}
		sandbox.Status.ObservedGeneration = sandbox.Generation
		meta.SetStatusCondition(&sandbox.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionTrue,
			Reason:             "Running",
			Message:            "Sandbox MicroVM is running",
			ObservedGeneration: sandbox.Generation,
		})
		if err := r.Status().Update(ctx, sandbox); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: requeueRunning}, nil
	}

	if pod.Status.Phase == corev1.PodFailed {
		logger.Info("Launcher Pod failed during Initializing")
		return r.setPhase(ctx, sandbox, sandboxv1alpha1.SandboxPhaseFailed,
			"PodFailed", "Launcher Pod entered Failed phase during initialization")
	}

	return ctrl.Result{RequeueAfter: requeueInitializing}, nil
}

// reconcileRunning handles the Running phase: check for timeout or deletion.
func (r *SandboxReconciler) reconcileRunning(ctx context.Context, sandbox *sandboxv1alpha1.Sandbox) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Check for timeout.
	if sandbox.Status.StartTime != nil && sandbox.Spec.Lifecycle.TimeoutSeconds > 0 {
		deadline := sandbox.Status.StartTime.Add(
			time.Duration(sandbox.Spec.Lifecycle.TimeoutSeconds) * time.Second)
		if time.Now().After(deadline) {
			logger.Info("Sandbox timed out, moving to Killing")
			return r.setPhase(ctx, sandbox, sandboxv1alpha1.SandboxPhaseKilling,
				"Timeout", "Sandbox exceeded its maximum lifetime")
		}
		remaining := time.Until(deadline)
		if remaining < requeueRunning {
			return ctrl.Result{RequeueAfter: remaining + time.Second}, nil
		}
	}

	// Check if the launcher Pod has unexpectedly terminated.
	pod := &corev1.Pod{}
	podName := launcherPodName(sandbox.Name)
	if err := r.Get(ctx, types.NamespacedName{Name: podName, Namespace: sandbox.Namespace}, pod); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Launcher Pod disappeared during Running, moving to Failed")
			return r.setPhase(ctx, sandbox, sandboxv1alpha1.SandboxPhaseFailed,
				"PodMissing", "Launcher Pod was deleted unexpectedly")
		}
		return ctrl.Result{}, err
	}

	if pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded {
		logger.Info("Launcher Pod terminated", "phase", pod.Status.Phase)
		return r.setPhase(ctx, sandbox, sandboxv1alpha1.SandboxPhaseKilling,
			"PodTerminated", fmt.Sprintf("Launcher Pod entered %s phase", pod.Status.Phase))
	}

	return ctrl.Result{RequeueAfter: requeueRunning}, nil
}

// reconcileKilling handles the Killing phase: delete the Pod and remove the finalizer.
func (r *SandboxReconciler) reconcileKilling(ctx context.Context, sandbox *sandboxv1alpha1.Sandbox) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	pod := &corev1.Pod{}
	podName := launcherPodName(sandbox.Name)
	if err := r.Get(ctx, types.NamespacedName{Name: podName, Namespace: sandbox.Namespace}, pod); err == nil {
		logger.Info("Deleting launcher Pod", "pod", podName)
		if err := r.Delete(ctx, pod); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to delete launcher pod: %w", err)
		}
	} else if !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}

	// Remove finalizer only after Pod is gone.
	if controllerutil.ContainsFinalizer(sandbox, SandboxFinalizer) {
		controllerutil.RemoveFinalizer(sandbox, SandboxFinalizer)
		if err := r.Update(ctx, sandbox); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// reconcileDelete handles deletion when DeletionTimestamp is set.
func (r *SandboxReconciler) reconcileDelete(ctx context.Context, sandbox *sandboxv1alpha1.Sandbox) (ctrl.Result, error) {
	if sandbox.Status.Phase != sandboxv1alpha1.SandboxPhaseKilling {
		sandbox.Status.Phase = sandboxv1alpha1.SandboxPhaseKilling
		sandbox.Status.ObservedGeneration = sandbox.Generation
		if err := r.Status().Update(ctx, sandbox); err != nil {
			return ctrl.Result{}, err
		}
	}
	return r.reconcileKilling(ctx, sandbox)
}

// setPhase is a helper that updates only the Sandbox phase and optionally adds a condition.
func (r *SandboxReconciler) setPhase(
	ctx context.Context,
	sandbox *sandboxv1alpha1.Sandbox,
	phase sandboxv1alpha1.SandboxPhase,
	reason, message string,
) (ctrl.Result, error) {
	sandbox.Status.Phase = phase
	sandbox.Status.ObservedGeneration = sandbox.Generation

	condStatus := metav1.ConditionFalse
	if phase == sandboxv1alpha1.SandboxPhaseRunning {
		condStatus = metav1.ConditionTrue
	}
	meta.SetStatusCondition(&sandbox.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             condStatus,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: sandbox.Generation,
	})

	if err := r.Status().Update(ctx, sandbox); err != nil {
		return ctrl.Result{}, err
	}

	// If we just moved to Killing, kick off the killing logic immediately.
	if phase == sandboxv1alpha1.SandboxPhaseKilling {
		return r.reconcileKilling(ctx, sandbox)
	}

	return ctrl.Result{}, nil
}
