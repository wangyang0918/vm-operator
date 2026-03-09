// Package v1alpha1 contains the Sandbox API types and webhook validation logic.
//
// +kubebuilder:webhook:path=/validate-sandbox-e2b-io-v1alpha1-sandbox,mutating=false,failurePolicy=fail,sideEffects=None,groups=sandbox.e2b.io,resources=sandboxes,verbs=create;update,versions=v1alpha1,name=vsandbox.kb.io,admissionReviewVersions=v1
package v1alpha1

import (
	"context"
	"fmt"
	"regexp"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var sandboxlog = logf.Log.WithName("sandbox-webhook")

// versionPattern matches semantic version strings like v1.2.3, 1.2, v1.4.0-dev, 5.10.68.
var versionPattern = regexp.MustCompile(`^v?\d+\.\d+[\w.\-]*$`)

// SandboxCustomValidator validates Sandbox resources on admission.
type SandboxCustomValidator struct{}

// SetupSandboxWebhookWithManager registers the validation webhook for Sandbox with the Manager.
func SetupSandboxWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(&Sandbox{}).
		WithValidator(&SandboxCustomValidator{}).
		Complete()
}

// ValidateCreate validates a newly created Sandbox.
func (v *SandboxCustomValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	sandbox, ok := obj.(*Sandbox)
	if !ok {
		return nil, fmt.Errorf("expected a Sandbox object but got %T", obj)
	}
	sandboxlog.Info("validate create", "name", sandbox.Name)
	if errs := validateSandboxSpec(&sandbox.Spec, field.NewPath("spec")); len(errs) > 0 {
		return nil, errs.ToAggregate()
	}
	return nil, nil
}

// ValidateUpdate validates an update to an existing Sandbox.
func (v *SandboxCustomValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	newSandbox, ok := newObj.(*Sandbox)
	if !ok {
		return nil, fmt.Errorf("expected a Sandbox object but got %T", newObj)
	}
	oldSandbox, ok := oldObj.(*Sandbox)
	if !ok {
		return nil, fmt.Errorf("expected a Sandbox object but got %T", oldObj)
	}
	sandboxlog.Info("validate update", "name", newSandbox.Name)

	var allErrs field.ErrorList
	allErrs = append(allErrs, validateSandboxSpec(&newSandbox.Spec, field.NewPath("spec"))...)
	allErrs = append(allErrs, validatePauseTransition(oldSandbox, newSandbox)...)
	if len(allErrs) > 0 {
		return nil, allErrs.ToAggregate()
	}
	return nil, nil
}

// ValidateDelete validates deletion of a Sandbox (no restrictions).
func (v *SandboxCustomValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// validateSandboxSpec runs all field-level validation rules against a SandboxSpec.
func validateSandboxSpec(spec *SandboxSpec, fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList
	allErrs = append(allErrs, validateResources(&spec.Resources, fldPath.Child("resources"))...)
	allErrs = append(allErrs, validateTemplate(&spec.Template, fldPath.Child("template"))...)
	allErrs = append(allErrs, validateRuntime(&spec.Runtime, fldPath.Child("runtime"))...)
	allErrs = append(allErrs, validateLifecycle(&spec.Lifecycle, fldPath.Child("lifecycle"))...)
	return allErrs
}

// validateResources checks vCPU, memory, and disk ranges.
func validateResources(r *ResourcesSpec, fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList
	if r.VCPU < 1 || r.VCPU > 64 {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("vcpu"), r.VCPU,
			"must be between 1 and 64"))
	}
	if r.MemoryMB < 128 || r.MemoryMB > 65536 {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("memoryMB"), r.MemoryMB,
			"must be between 128 and 65536"))
	}
	if r.DiskMB != 0 && r.DiskMB < 512 {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("diskMB"), r.DiskMB,
			"must be at least 512 when set"))
	}
	return allErrs
}

// validateTemplate checks that templateID is non-empty.
func validateTemplate(t *TemplateSpec, fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList
	if t.TemplateID == "" {
		allErrs = append(allErrs, field.Required(fldPath.Child("templateID"),
			"templateID is required"))
	}
	return allErrs
}

// validateRuntime checks that version strings, when provided, match a version format.
func validateRuntime(r *RuntimeSpec, fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList
	if r.FirecrackerVersion != "" && !versionPattern.MatchString(r.FirecrackerVersion) {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("firecrackerVersion"), r.FirecrackerVersion,
			"must be a valid version string (e.g. v1.3.3, 5.10.68)"))
	}
	if r.KernelVersion != "" && !versionPattern.MatchString(r.KernelVersion) {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("kernelVersion"), r.KernelVersion,
			"must be a valid version string (e.g. v1.3.3, 5.10.68)"))
	}
	return allErrs
}

// validateLifecycle checks that timeoutSeconds is non-negative.
func validateLifecycle(l *LifecycleSpec, fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList
	if l.TimeoutSeconds < 0 {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("timeoutSeconds"), l.TimeoutSeconds,
			"must be non-negative"))
	}
	return allErrs
}

// validatePauseTransition enforces legal pause/resume state transitions.
//
//   - Requesting pause (paused: false → true): only allowed when the sandbox is Running.
//   - Requesting resume (paused: true → false): only allowed when the sandbox is Paused or Pausing.
func validatePauseTransition(old, new *Sandbox) field.ErrorList {
	var allErrs field.ErrorList
	fldPath := field.NewPath("spec").Child("paused")

	if !old.Spec.Paused && new.Spec.Paused {
		// Requesting pause — sandbox must be Running.
		if old.Status.Phase != SandboxPhaseRunning {
			allErrs = append(allErrs, field.Forbidden(fldPath,
				fmt.Sprintf("cannot pause sandbox in phase %q; sandbox must be Running", old.Status.Phase)))
		}
	}

	if old.Spec.Paused && !new.Spec.Paused {
		// Requesting resume — sandbox must be Paused or Pausing.
		phase := old.Status.Phase
		if phase != SandboxPhasePaused && phase != SandboxPhasePausing {
			allErrs = append(allErrs, field.Forbidden(fldPath,
				fmt.Sprintf("cannot resume sandbox in phase %q; sandbox must be Paused or Pausing", phase)))
		}
	}

	return allErrs
}
