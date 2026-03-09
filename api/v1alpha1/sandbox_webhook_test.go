package v1alpha1

import (
	"context"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/validation/field"
)

// newValidSandbox returns a Sandbox that passes all validation rules.
func newValidSandbox() *Sandbox {
	return &Sandbox{
		Spec: SandboxSpec{
			Template: TemplateSpec{TemplateID: "tpl-001"},
			Resources: ResourcesSpec{
				VCPU:     2,
				MemoryMB: 512,
				DiskMB:   2048,
			},
			Runtime:   RuntimeSpec{FirecrackerVersion: "v1.3.3", KernelVersion: "5.10.68"},
			Lifecycle: LifecycleSpec{TimeoutSeconds: 300},
		},
	}
}

func TestSandboxCustomValidator_ValidateCreate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Sandbox)
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid sandbox",
			mutate:  func(_ *Sandbox) {},
			wantErr: false,
		},
		{
			name:    "missing templateID",
			mutate:  func(s *Sandbox) { s.Spec.Template.TemplateID = "" },
			wantErr: true,
			errMsg:  "templateID",
		},
		{
			name:    "vcpu below minimum",
			mutate:  func(s *Sandbox) { s.Spec.Resources.VCPU = 0 },
			wantErr: true,
			errMsg:  "vcpu",
		},
		{
			name:    "vcpu above maximum",
			mutate:  func(s *Sandbox) { s.Spec.Resources.VCPU = 65 },
			wantErr: true,
			errMsg:  "vcpu",
		},
		{
			name:    "vcpu at minimum boundary",
			mutate:  func(s *Sandbox) { s.Spec.Resources.VCPU = 1 },
			wantErr: false,
		},
		{
			name:    "vcpu at maximum boundary",
			mutate:  func(s *Sandbox) { s.Spec.Resources.VCPU = 64 },
			wantErr: false,
		},
		{
			name:    "memoryMB below minimum",
			mutate:  func(s *Sandbox) { s.Spec.Resources.MemoryMB = 64 },
			wantErr: true,
			errMsg:  "memoryMB",
		},
		{
			name:    "memoryMB above maximum",
			mutate:  func(s *Sandbox) { s.Spec.Resources.MemoryMB = 65537 },
			wantErr: true,
			errMsg:  "memoryMB",
		},
		{
			name:    "memoryMB at minimum boundary",
			mutate:  func(s *Sandbox) { s.Spec.Resources.MemoryMB = 128 },
			wantErr: false,
		},
		{
			name:    "memoryMB at maximum boundary",
			mutate:  func(s *Sandbox) { s.Spec.Resources.MemoryMB = 65536 },
			wantErr: false,
		},
		{
			name:    "diskMB too small when set",
			mutate:  func(s *Sandbox) { s.Spec.Resources.DiskMB = 256 },
			wantErr: true,
			errMsg:  "diskMB",
		},
		{
			name:    "diskMB at minimum when set",
			mutate:  func(s *Sandbox) { s.Spec.Resources.DiskMB = 512 },
			wantErr: false,
		},
		{
			name:    "diskMB zero is allowed",
			mutate:  func(s *Sandbox) { s.Spec.Resources.DiskMB = 0 },
			wantErr: false,
		},
		{
			name:    "invalid firecrackerVersion",
			mutate:  func(s *Sandbox) { s.Spec.Runtime.FirecrackerVersion = "latest" },
			wantErr: true,
			errMsg:  "firecrackerVersion",
		},
		{
			name:    "valid firecrackerVersion with v prefix",
			mutate:  func(s *Sandbox) { s.Spec.Runtime.FirecrackerVersion = "v1.4.0" },
			wantErr: false,
		},
		{
			name:    "valid firecrackerVersion without v prefix",
			mutate:  func(s *Sandbox) { s.Spec.Runtime.FirecrackerVersion = "1.4.0" },
			wantErr: false,
		},
		{
			name:    "valid firecrackerVersion with pre-release suffix",
			mutate:  func(s *Sandbox) { s.Spec.Runtime.FirecrackerVersion = "v1.4.0-dev" },
			wantErr: false,
		},
		{
			name:    "invalid kernelVersion",
			mutate:  func(s *Sandbox) { s.Spec.Runtime.KernelVersion = "not-a-version" },
			wantErr: true,
			errMsg:  "kernelVersion",
		},
		{
			name:    "valid kernelVersion",
			mutate:  func(s *Sandbox) { s.Spec.Runtime.KernelVersion = "5.15.0" },
			wantErr: false,
		},
		{
			name:    "empty runtime versions are allowed",
			mutate:  func(s *Sandbox) { s.Spec.Runtime = RuntimeSpec{} },
			wantErr: false,
		},
		{
			name:    "negative timeoutSeconds",
			mutate:  func(s *Sandbox) { s.Spec.Lifecycle.TimeoutSeconds = -1 },
			wantErr: true,
			errMsg:  "timeoutSeconds",
		},
		{
			name:    "zero timeoutSeconds is allowed",
			mutate:  func(s *Sandbox) { s.Spec.Lifecycle.TimeoutSeconds = 0 },
			wantErr: false,
		},
		{
			name:    "multiple validation errors reported together",
			mutate:  func(s *Sandbox) { s.Spec.Resources.VCPU = 0; s.Spec.Resources.MemoryMB = 64 },
			wantErr: true,
			errMsg:  "vcpu",
		},
	}

	v := &SandboxCustomValidator{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sandbox := newValidSandbox()
			tt.mutate(sandbox)

			_, err := v.ValidateCreate(context.Background(), sandbox)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateCreate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("ValidateCreate() error = %q, want it to contain %q", err.Error(), tt.errMsg)
			}
		})
	}
}

func TestSandboxCustomValidator_ValidateCreate_WrongType(t *testing.T) {
	v := &SandboxCustomValidator{}
	_, err := v.ValidateCreate(context.Background(), &SandboxList{})
	if err == nil {
		t.Error("ValidateCreate() expected error for wrong type, got nil")
	}
}

func TestSandboxCustomValidator_ValidateUpdate(t *testing.T) {
	tests := []struct {
		name      string
		oldMutate func(*Sandbox)
		newMutate func(*Sandbox)
		wantErr   bool
		errMsg    string
	}{
		{
			name:      "valid update with no phase change",
			oldMutate: func(_ *Sandbox) {},
			newMutate: func(s *Sandbox) { s.Spec.Resources.MemoryMB = 1024 },
			wantErr:   false,
		},
		{
			name:      "valid update — spec validation errors still apply",
			oldMutate: func(_ *Sandbox) {},
			newMutate: func(s *Sandbox) { s.Spec.Resources.VCPU = 0 },
			wantErr:   true,
			errMsg:    "vcpu",
		},
		{
			name: "pause allowed when Running",
			oldMutate: func(s *Sandbox) {
				s.Spec.Paused = false
				s.Status.Phase = SandboxPhaseRunning
			},
			newMutate: func(s *Sandbox) { s.Spec.Paused = true },
			wantErr:   false,
		},
		{
			name: "pause rejected when not Running",
			oldMutate: func(s *Sandbox) {
				s.Spec.Paused = false
				s.Status.Phase = SandboxPhasePending
			},
			newMutate: func(s *Sandbox) { s.Spec.Paused = true },
			wantErr:   true,
			errMsg:    "paused",
		},
		{
			name: "pause rejected when Initializing",
			oldMutate: func(s *Sandbox) {
				s.Spec.Paused = false
				s.Status.Phase = SandboxPhaseInitializing
			},
			newMutate: func(s *Sandbox) { s.Spec.Paused = true },
			wantErr:   true,
			errMsg:    "paused",
		},
		{
			name: "resume allowed when Paused",
			oldMutate: func(s *Sandbox) {
				s.Spec.Paused = true
				s.Status.Phase = SandboxPhasePaused
			},
			newMutate: func(s *Sandbox) { s.Spec.Paused = false },
			wantErr:   false,
		},
		{
			name: "resume allowed when Pausing",
			oldMutate: func(s *Sandbox) {
				s.Spec.Paused = true
				s.Status.Phase = SandboxPhasePausing
			},
			newMutate: func(s *Sandbox) { s.Spec.Paused = false },
			wantErr:   false,
		},
		{
			name: "resume rejected when Running",
			oldMutate: func(s *Sandbox) {
				s.Spec.Paused = true
				s.Status.Phase = SandboxPhaseRunning
			},
			newMutate: func(s *Sandbox) { s.Spec.Paused = false },
			wantErr:   true,
			errMsg:    "paused",
		},
		{
			name: "no transition when paused unchanged",
			oldMutate: func(s *Sandbox) {
				s.Spec.Paused = false
				s.Status.Phase = SandboxPhasePending
			},
			newMutate: func(_ *Sandbox) {},
			wantErr:   false,
		},
	}

	v := &SandboxCustomValidator{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldSandbox := newValidSandbox()
			tt.oldMutate(oldSandbox)

			newSandbox := newValidSandbox()
			// Carry over status from oldSandbox so new has same phase unless overridden.
			newSandbox.Status = oldSandbox.Status
			tt.newMutate(newSandbox)

			_, err := v.ValidateUpdate(context.Background(), oldSandbox, newSandbox)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateUpdate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("ValidateUpdate() error = %q, want it to contain %q", err.Error(), tt.errMsg)
			}
		})
	}
}

func TestSandboxCustomValidator_ValidateUpdate_WrongType(t *testing.T) {
	v := &SandboxCustomValidator{}
	sandbox := newValidSandbox()

	// Wrong newObj type
	_, err := v.ValidateUpdate(context.Background(), sandbox, &SandboxList{})
	if err == nil {
		t.Error("ValidateUpdate() expected error for wrong newObj type, got nil")
	}

	// Wrong oldObj type
	_, err = v.ValidateUpdate(context.Background(), &SandboxList{}, sandbox)
	if err == nil {
		t.Error("ValidateUpdate() expected error for wrong oldObj type, got nil")
	}
}

func TestSandboxCustomValidator_ValidateDelete(t *testing.T) {
	v := &SandboxCustomValidator{}
	sandbox := newValidSandbox()
	_, err := v.ValidateDelete(context.Background(), sandbox)
	if err != nil {
		t.Errorf("ValidateDelete() unexpected error: %v", err)
	}
}

func TestValidateResources(t *testing.T) {
	tests := []struct {
		name    string
		r       ResourcesSpec
		wantErr bool
	}{
		{name: "valid", r: ResourcesSpec{VCPU: 4, MemoryMB: 1024, DiskMB: 4096}, wantErr: false},
		{name: "vcpu=0", r: ResourcesSpec{VCPU: 0, MemoryMB: 512}, wantErr: true},
		{name: "vcpu=65", r: ResourcesSpec{VCPU: 65, MemoryMB: 512}, wantErr: true},
		{name: "memory=127", r: ResourcesSpec{VCPU: 2, MemoryMB: 127}, wantErr: true},
		{name: "memory=65537", r: ResourcesSpec{VCPU: 2, MemoryMB: 65537}, wantErr: true},
		{name: "disk=256", r: ResourcesSpec{VCPU: 2, MemoryMB: 512, DiskMB: 256}, wantErr: true},
		{name: "disk=0 (omitted)", r: ResourcesSpec{VCPU: 2, MemoryMB: 512, DiskMB: 0}, wantErr: false},
		{name: "disk=512", r: ResourcesSpec{VCPU: 2, MemoryMB: 512, DiskMB: 512}, wantErr: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := validateResources(&tt.r, fieldPath("resources"))
			if (len(errs) > 0) != tt.wantErr {
				t.Errorf("validateResources() errors=%v, wantErr=%v", errs, tt.wantErr)
			}
		})
	}
}

func TestValidateTemplate(t *testing.T) {
	tests := []struct {
		name    string
		t       TemplateSpec
		wantErr bool
	}{
		{name: "valid", t: TemplateSpec{TemplateID: "tpl-001"}, wantErr: false},
		{name: "empty templateID", t: TemplateSpec{TemplateID: ""}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := validateTemplate(&tt.t, fieldPath("template"))
			if (len(errs) > 0) != tt.wantErr {
				t.Errorf("validateTemplate() errors=%v, wantErr=%v", errs, tt.wantErr)
			}
		})
	}
}

func TestValidateRuntime(t *testing.T) {
	tests := []struct {
		name    string
		r       RuntimeSpec
		wantErr bool
	}{
		{name: "empty versions", r: RuntimeSpec{}, wantErr: false},
		{name: "valid firecracker v-prefix", r: RuntimeSpec{FirecrackerVersion: "v1.3.3"}, wantErr: false},
		{name: "valid firecracker no-prefix", r: RuntimeSpec{FirecrackerVersion: "1.4.0"}, wantErr: false},
		{name: "valid firecracker pre-release", r: RuntimeSpec{FirecrackerVersion: "v1.4.0-dev"}, wantErr: false},
		{name: "invalid firecracker", r: RuntimeSpec{FirecrackerVersion: "latest"}, wantErr: true},
		{name: "valid kernel", r: RuntimeSpec{KernelVersion: "5.10.68"}, wantErr: false},
		{name: "invalid kernel", r: RuntimeSpec{KernelVersion: "my-custom-kernel"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := validateRuntime(&tt.r, fieldPath("runtime"))
			if (len(errs) > 0) != tt.wantErr {
				t.Errorf("validateRuntime() errors=%v, wantErr=%v", errs, tt.wantErr)
			}
		})
	}
}

func TestValidateLifecycle(t *testing.T) {
	tests := []struct {
		name    string
		l       LifecycleSpec
		wantErr bool
	}{
		{name: "valid timeout", l: LifecycleSpec{TimeoutSeconds: 300}, wantErr: false},
		{name: "zero timeout", l: LifecycleSpec{TimeoutSeconds: 0}, wantErr: false},
		{name: "negative timeout", l: LifecycleSpec{TimeoutSeconds: -1}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := validateLifecycle(&tt.l, fieldPath("lifecycle"))
			if (len(errs) > 0) != tt.wantErr {
				t.Errorf("validateLifecycle() errors=%v, wantErr=%v", errs, tt.wantErr)
			}
		})
	}
}

func TestValidatePauseTransition(t *testing.T) {
	makeOld := func(paused bool, phase SandboxPhase) *Sandbox {
		s := newValidSandbox()
		s.Spec.Paused = paused
		s.Status.Phase = phase
		return s
	}
	makeNew := func(paused bool) *Sandbox {
		s := newValidSandbox()
		s.Spec.Paused = paused
		return s
	}

	tests := []struct {
		name    string
		old     *Sandbox
		new     *Sandbox
		wantErr bool
	}{
		{"no transition", makeOld(false, SandboxPhaseRunning), makeNew(false), false},
		{"pause from Running ok", makeOld(false, SandboxPhaseRunning), makeNew(true), false},
		{"pause from Pending rejected", makeOld(false, SandboxPhasePending), makeNew(true), true},
		{"pause from Scheduling rejected", makeOld(false, SandboxPhaseScheduling), makeNew(true), true},
		{"pause from Initializing rejected", makeOld(false, SandboxPhaseInitializing), makeNew(true), true},
		{"pause from Failed rejected", makeOld(false, SandboxPhaseFailed), makeNew(true), true},
		{"resume from Paused ok", makeOld(true, SandboxPhasePaused), makeNew(false), false},
		{"resume from Pausing ok", makeOld(true, SandboxPhasePausing), makeNew(false), false},
		{"resume from Running rejected", makeOld(true, SandboxPhaseRunning), makeNew(false), true},
		{"resume from Killing rejected", makeOld(true, SandboxPhaseKilling), makeNew(false), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := validatePauseTransition(tt.old, tt.new)
			if (len(errs) > 0) != tt.wantErr {
				t.Errorf("validatePauseTransition() errors=%v, wantErr=%v", errs, tt.wantErr)
			}
		})
	}
}

// fieldPath is a test helper that creates a simple field.Path.
func fieldPath(name string) *field.Path {
	return field.NewPath(name)
}
