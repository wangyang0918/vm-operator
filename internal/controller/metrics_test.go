package controller

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	sandboxv1alpha1 "github.com/wangyang0918/vm-operator/api/v1alpha1"
)

// newTestMetrics creates a SandboxMetrics backed by a fresh, isolated registry.
// Using a dedicated registry avoids "already registered" panics when tests run in parallel.
func newTestMetrics(t *testing.T) *SandboxMetrics {
	t.Helper()
	return NewSandboxMetrics(prometheus.NewRegistry())
}

// gaugeValue gathers all metrics from the given GaugeVec and returns the value
// for the series identified by labelValues, or 0 if no such series exists.
func gaugeValue(t *testing.T, g *prometheus.GaugeVec, labelValues ...string) float64 {
	t.Helper()
	m := &dto.Metric{}
	if err := g.WithLabelValues(labelValues...).Write(m); err != nil {
		t.Fatalf("failed to read gauge: %v", err)
	}
	return m.GetGauge().GetValue()
}

// counterValue returns the current value of the counter series identified by labelValues.
func counterValue(t *testing.T, c *prometheus.CounterVec, labelValues ...string) float64 {
	t.Helper()
	m := &dto.Metric{}
	if err := c.WithLabelValues(labelValues...).Write(m); err != nil {
		t.Fatalf("failed to read counter: %v", err)
	}
	return m.GetCounter().GetValue()
}

// ---------------------------------------------------------------------------
// Phase gauge tests
// ---------------------------------------------------------------------------

func TestRecordPhaseTransition_FirstTransition(t *testing.T) {
	m := newTestMetrics(t)

	// First transition has no previous phase.
	m.RecordPhaseTransition("", sandboxv1alpha1.SandboxPhasePending)

	if got := gaugeValue(t, m.phaseGauge, string(sandboxv1alpha1.SandboxPhasePending)); got != 1 {
		t.Errorf("phaseGauge[Pending] = %v, want 1", got)
	}
}

func TestRecordPhaseTransition_SubsequentTransition(t *testing.T) {
	m := newTestMetrics(t)

	m.RecordPhaseTransition("", sandboxv1alpha1.SandboxPhasePending)
	m.RecordPhaseTransition(sandboxv1alpha1.SandboxPhasePending, sandboxv1alpha1.SandboxPhaseScheduling)

	if got := gaugeValue(t, m.phaseGauge, string(sandboxv1alpha1.SandboxPhasePending)); got != 0 {
		t.Errorf("phaseGauge[Pending] = %v, want 0 after leaving Pending", got)
	}
	if got := gaugeValue(t, m.phaseGauge, string(sandboxv1alpha1.SandboxPhaseScheduling)); got != 1 {
		t.Errorf("phaseGauge[Scheduling] = %v, want 1", got)
	}
}

func TestRecordPhaseTransition_MultipleSandboxes(t *testing.T) {
	m := newTestMetrics(t)

	// Two sandboxes both reach Scheduling.
	m.RecordPhaseTransition("", sandboxv1alpha1.SandboxPhasePending)
	m.RecordPhaseTransition(sandboxv1alpha1.SandboxPhasePending, sandboxv1alpha1.SandboxPhaseScheduling)
	m.RecordPhaseTransition("", sandboxv1alpha1.SandboxPhasePending)
	m.RecordPhaseTransition(sandboxv1alpha1.SandboxPhasePending, sandboxv1alpha1.SandboxPhaseScheduling)

	if got := gaugeValue(t, m.phaseGauge, string(sandboxv1alpha1.SandboxPhaseScheduling)); got != 2 {
		t.Errorf("phaseGauge[Scheduling] = %v, want 2", got)
	}
}

func TestRecordPhaseTransition_NilReceiver(t *testing.T) {
	var m *SandboxMetrics
	// Should not panic.
	m.RecordPhaseTransition("", sandboxv1alpha1.SandboxPhasePending)
}

// ---------------------------------------------------------------------------
// Runtime gauge tests
// ---------------------------------------------------------------------------

func TestRecordRuntime(t *testing.T) {
	m := newTestMetrics(t)

	start := time.Now().Add(-10 * time.Second)
	m.RecordRuntime("sbx-1", "default", start)

	got := gaugeValue(t, m.runtimeSeconds, "sbx-1", "default")
	// Allow ±2 s of clock drift in the test environment.
	if got < 8 || got > 12 {
		t.Errorf("runtimeSeconds[sbx-1,default] = %v, want ~10", got)
	}
}

func TestClearRuntime(t *testing.T) {
	m := newTestMetrics(t)

	m.RecordRuntime("sbx-1", "default", time.Now().Add(-5*time.Second))
	m.ClearRuntime("sbx-1", "default")

	// After deletion, the gauge should reset to zero when re-observed.
	if got := gaugeValue(t, m.runtimeSeconds, "sbx-1", "default"); got != 0 {
		t.Errorf("runtimeSeconds after ClearRuntime = %v, want 0", got)
	}
}

func TestRecordRuntime_NilReceiver(t *testing.T) {
	var m *SandboxMetrics
	m.RecordRuntime("sbx-1", "default", time.Now())
}

// ---------------------------------------------------------------------------
// Launcher success / failure counter tests
// ---------------------------------------------------------------------------

func TestRecordLauncherSuccess(t *testing.T) {
	m := newTestMetrics(t)

	m.RecordLauncherSuccess("default")
	m.RecordLauncherSuccess("default")

	if got := counterValue(t, m.launcherSuccessTotal, "default"); got != 2 {
		t.Errorf("launcherSuccessTotal = %v, want 2", got)
	}
}

func TestRecordLauncherFailure(t *testing.T) {
	m := newTestMetrics(t)

	m.RecordLauncherFailure("kube-system")

	if got := counterValue(t, m.launcherFailureTotal, "kube-system"); got != 1 {
		t.Errorf("launcherFailureTotal = %v, want 1", got)
	}
}

func TestRecordLauncherSuccess_NilReceiver(t *testing.T) {
	var m *SandboxMetrics
	m.RecordLauncherSuccess("default")
}

// ---------------------------------------------------------------------------
// Pause / resume counter tests
// ---------------------------------------------------------------------------

func TestRecordPause(t *testing.T) {
	m := newTestMetrics(t)

	m.RecordPause("default")
	m.RecordPause("default")
	m.RecordPause("other")

	if got := counterValue(t, m.pauseTotal, "default"); got != 2 {
		t.Errorf("pauseTotal[default] = %v, want 2", got)
	}
	if got := counterValue(t, m.pauseTotal, "other"); got != 1 {
		t.Errorf("pauseTotal[other] = %v, want 1", got)
	}
}

func TestRecordResume(t *testing.T) {
	m := newTestMetrics(t)

	m.RecordResume("default")

	if got := counterValue(t, m.resumeTotal, "default"); got != 1 {
		t.Errorf("resumeTotal = %v, want 1", got)
	}
}

// ---------------------------------------------------------------------------
// Error counter tests
// ---------------------------------------------------------------------------

func TestRecordError(t *testing.T) {
	m := newTestMetrics(t)

	m.RecordError("default", "PodFailed")
	m.RecordError("default", "PodFailed")
	m.RecordError("default", "Unschedulable")

	if got := counterValue(t, m.errorTotal, "default", "PodFailed"); got != 2 {
		t.Errorf("errorTotal[PodFailed] = %v, want 2", got)
	}
	if got := counterValue(t, m.errorTotal, "default", "Unschedulable"); got != 1 {
		t.Errorf("errorTotal[Unschedulable] = %v, want 1", got)
	}
}

func TestRecordError_NilReceiver(t *testing.T) {
	var m *SandboxMetrics
	m.RecordError("default", "PodFailed")
}

// ---------------------------------------------------------------------------
// NewSandboxMetrics registration test
// ---------------------------------------------------------------------------

func TestNewSandboxMetrics_RegistersAllMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewSandboxMetrics(reg)

	if m == nil {
		t.Fatal("NewSandboxMetrics returned nil")
	}

	// Gather all metrics and verify we have 7 metric families registered.
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error: %v", err)
	}

	wantNames := map[string]bool{
		"sandbox_operator_sandboxes_by_phase":      true,
		"sandbox_operator_sandbox_runtime_seconds": true,
		"sandbox_operator_launcher_success_total":  true,
		"sandbox_operator_launcher_failure_total":  true,
		"sandbox_operator_pause_total":             true,
		"sandbox_operator_resume_total":            true,
		"sandbox_operator_error_total":             true,
	}

	// Trigger at least one observation so the metric family is present in the output.
	m.RecordPhaseTransition("", sandboxv1alpha1.SandboxPhasePending)
	m.RecordLauncherSuccess("default")
	m.RecordLauncherFailure("default")
	m.RecordPause("default")
	m.RecordResume("default")
	m.RecordError("default", "PodFailed")
	m.RecordRuntime("sbx", "default", time.Now().Add(-1*time.Second))

	mfs, err = reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error after recording: %v", err)
	}

	got := make(map[string]bool)
	for _, mf := range mfs {
		got[mf.GetName()] = true
	}

	for name := range wantNames {
		if !got[name] {
			t.Errorf("metric family %q not found in gathered output", name)
		}
	}
}
