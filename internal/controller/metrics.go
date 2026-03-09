package controller

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"

	sandboxv1alpha1 "github.com/wangyang0918/vm-operator/api/v1alpha1"
)

// SandboxMetrics holds all Prometheus metrics for the sandbox controller.
type SandboxMetrics struct {
	// phaseGauge tracks the number of sandboxes currently in each phase.
	phaseGauge *prometheus.GaugeVec

	// runtimeSeconds tracks how long each sandbox has been in Running state.
	runtimeSeconds *prometheus.GaugeVec

	// launcherSuccessTotal counts launcher pods that successfully reached Running.
	launcherSuccessTotal *prometheus.CounterVec

	// launcherFailureTotal counts launcher pods that failed to start.
	launcherFailureTotal *prometheus.CounterVec

	// pauseTotal counts sandbox pause operations.
	pauseTotal *prometheus.CounterVec

	// resumeTotal counts sandbox resume operations.
	resumeTotal *prometheus.CounterVec

	// errorTotal counts transitions to the Failed phase.
	errorTotal *prometheus.CounterVec
}

// NewSandboxMetrics creates and registers all sandbox metrics with the provided Prometheus
// Registerer. Pass sigs.k8s.io/controller-runtime/pkg/metrics.Registry for production use,
// or a fresh prometheus.NewRegistry() in unit tests to avoid duplicate-registration panics.
func NewSandboxMetrics(reg prometheus.Registerer) *SandboxMetrics {
	m := &SandboxMetrics{
		phaseGauge: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "sandbox_operator",
				Name:      "sandboxes_by_phase",
				Help:      "Number of sandboxes currently in each lifecycle phase.",
			},
			[]string{"phase"},
		),
		runtimeSeconds: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "sandbox_operator",
				Name:      "sandbox_runtime_seconds",
				Help:      "Duration in seconds that a sandbox has been in the Running state.",
			},
			[]string{"sandbox", "namespace"},
		),
		launcherSuccessTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "sandbox_operator",
				Name:      "launcher_success_total",
				Help:      "Total number of launcher pods that successfully reached Running state.",
			},
			[]string{"namespace"},
		),
		launcherFailureTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "sandbox_operator",
				Name:      "launcher_failure_total",
				Help:      "Total number of launcher pods that failed to start.",
			},
			[]string{"namespace"},
		),
		pauseTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "sandbox_operator",
				Name:      "pause_total",
				Help:      "Total number of sandbox pause operations.",
			},
			[]string{"namespace"},
		),
		resumeTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "sandbox_operator",
				Name:      "resume_total",
				Help:      "Total number of sandbox resume operations.",
			},
			[]string{"namespace"},
		),
		errorTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "sandbox_operator",
				Name:      "error_total",
				Help:      "Total number of sandbox transitions to the Failed phase.",
			},
			[]string{"namespace", "reason"},
		),
	}

	reg.MustRegister(
		m.phaseGauge,
		m.runtimeSeconds,
		m.launcherSuccessTotal,
		m.launcherFailureTotal,
		m.pauseTotal,
		m.resumeTotal,
		m.errorTotal,
	)

	return m
}

// RecordPhaseTransition decrements the old phase gauge and increments the new one.
// An empty oldPhase (first transition) only increments the new gauge.
func (m *SandboxMetrics) RecordPhaseTransition(oldPhase, newPhase sandboxv1alpha1.SandboxPhase) {
	if m == nil {
		return
	}
	if oldPhase != "" {
		m.phaseGauge.WithLabelValues(string(oldPhase)).Dec()
	}
	m.phaseGauge.WithLabelValues(string(newPhase)).Inc()
}

// RecordRuntime sets the runtime gauge for the given sandbox to the number of seconds
// elapsed since startTime. Call this each time a Running sandbox is reconciled.
func (m *SandboxMetrics) RecordRuntime(sandbox, namespace string, startTime time.Time) {
	if m == nil {
		return
	}
	m.runtimeSeconds.WithLabelValues(sandbox, namespace).Set(time.Since(startTime).Seconds())
}

// ClearRuntime removes the runtime gauge for the given sandbox.
// Call this when a sandbox leaves the Running state.
func (m *SandboxMetrics) ClearRuntime(sandbox, namespace string) {
	if m == nil {
		return
	}
	m.runtimeSeconds.DeleteLabelValues(sandbox, namespace)
}

// RecordLauncherSuccess increments the launcher-success counter.
func (m *SandboxMetrics) RecordLauncherSuccess(namespace string) {
	if m == nil {
		return
	}
	m.launcherSuccessTotal.WithLabelValues(namespace).Inc()
}

// RecordLauncherFailure increments the launcher-failure counter.
func (m *SandboxMetrics) RecordLauncherFailure(namespace string) {
	if m == nil {
		return
	}
	m.launcherFailureTotal.WithLabelValues(namespace).Inc()
}

// RecordPause increments the pause counter.
func (m *SandboxMetrics) RecordPause(namespace string) {
	if m == nil {
		return
	}
	m.pauseTotal.WithLabelValues(namespace).Inc()
}

// RecordResume increments the resume counter.
func (m *SandboxMetrics) RecordResume(namespace string) {
	if m == nil {
		return
	}
	m.resumeTotal.WithLabelValues(namespace).Inc()
}

// RecordError increments the error counter with the given failure reason.
func (m *SandboxMetrics) RecordError(namespace, reason string) {
	if m == nil {
		return
	}
	m.errorTotal.WithLabelValues(namespace, reason).Inc()
}
