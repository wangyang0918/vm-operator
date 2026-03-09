package controller

import (
	"context"
	"testing"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	sandboxv1alpha1 "github.com/wangyang0918/vm-operator/api/v1alpha1"
)

// newSchemeWithNetworking creates a runtime.Scheme with all required types including NetworkPolicy.
// clientgoscheme already registers networking/v1 types so no separate call is needed.
func newSchemeWithNetworking(t *testing.T) *runtime.Scheme {
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

// newReconcilerWithNetworking creates a SandboxReconciler backed by a fake client that
// also has the NetworkPolicy type registered.
func newReconcilerWithNetworking(t *testing.T, objs ...runtime.Object) *SandboxReconciler {
	t.Helper()
	s := newSchemeWithNetworking(t)
	builder := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&sandboxv1alpha1.Sandbox{})
	for _, o := range objs {
		builder = builder.WithRuntimeObjects(o)
	}
	return &SandboxReconciler{
		Client: builder.Build(),
		Scheme: s,
		// Metrics is nil; all SandboxMetrics methods handle nil receivers.
	}
}

// getNetworkPolicy fetches a NetworkPolicy from the fake client.
func getNetworkPolicy(t *testing.T, r *SandboxReconciler, name, namespace string) (*networkingv1.NetworkPolicy, bool) {
	t.Helper()
	np := &networkingv1.NetworkPolicy{}
	err := r.Get(context.Background(), types.NamespacedName{Name: name, Namespace: namespace}, np)
	if err != nil {
		return nil, false
	}
	return np, true
}

// TestNetworkPolicy_CreatedWhenIsolationDefault verifies that a NetworkPolicy is created
// for a Sandbox with isolationPolicy=Default.
func TestNetworkPolicy_CreatedWhenIsolationDefault(t *testing.T) {
	sandbox := newSandbox("test-sbx", "default")
	sandbox.Finalizers = []string{SandboxFinalizer}
	sandbox.Status.Phase = sandboxv1alpha1.SandboxPhasePending
	sandbox.Spec.NetworkPolicy = sandboxv1alpha1.NetworkPolicySpec{
		IsolationPolicy: sandboxv1alpha1.NetworkPolicyIsolationDefault,
	}

	r := newReconcilerWithNetworking(t, sandbox)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-sbx", Namespace: "default"}}

	_, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	npName := networkPolicyName("test-sbx")
	np, ok := getNetworkPolicy(t, r, npName, "default")
	if !ok {
		t.Fatalf("expected NetworkPolicy %q to be created", npName)
	}

	// Verify the pod selector targets this sandbox's launcher Pod.
	if np.Spec.PodSelector.MatchLabels[LabelSandboxName] != "test-sbx" {
		t.Errorf("expected PodSelector to match sandbox-name=test-sbx, got %v",
			np.Spec.PodSelector.MatchLabels)
	}

	// Verify the policy type includes Ingress.
	if len(np.Spec.PolicyTypes) == 0 || np.Spec.PolicyTypes[0] != networkingv1.PolicyTypeIngress {
		t.Errorf("expected PolicyTypes to contain Ingress, got %v", np.Spec.PolicyTypes)
	}

	// Verify there are two ingress rules (non-launcher and same-sandbox).
	if len(np.Spec.Ingress) != 2 {
		t.Errorf("expected 2 ingress rules, got %d", len(np.Spec.Ingress))
	}
}

// TestNetworkPolicy_NotCreatedWhenIsolationNone verifies that no NetworkPolicy is created
// for a Sandbox with the default (None) isolation policy.
func TestNetworkPolicy_NotCreatedWhenIsolationNone(t *testing.T) {
	sandbox := newSandbox("test-sbx", "default")
	sandbox.Finalizers = []string{SandboxFinalizer}
	sandbox.Status.Phase = sandboxv1alpha1.SandboxPhasePending
	// IsolationPolicy defaults to None (zero value).

	r := newReconcilerWithNetworking(t, sandbox)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-sbx", Namespace: "default"}}

	_, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	npName := networkPolicyName("test-sbx")
	if _, ok := getNetworkPolicy(t, r, npName, "default"); ok {
		t.Errorf("expected no NetworkPolicy to exist when IsolationPolicy is None, but found one")
	}
}

// TestNetworkPolicy_DeletedWhenIsolationChangedToNone verifies that an existing
// NetworkPolicy is deleted when a Sandbox's isolationPolicy is changed from Default to None.
func TestNetworkPolicy_DeletedWhenIsolationChangedToNone(t *testing.T) {
	sandbox := newSandbox("test-sbx", "default")
	sandbox.Finalizers = []string{SandboxFinalizer}
	sandbox.Status.Phase = sandboxv1alpha1.SandboxPhaseRunning
	// Start with isolation disabled.
	sandbox.Spec.NetworkPolicy = sandboxv1alpha1.NetworkPolicySpec{
		IsolationPolicy: sandboxv1alpha1.NetworkPolicyIsolationNone,
	}

	// Pre-create a NetworkPolicy to simulate a previously enabled isolation.
	existingNP := buildNetworkPolicy(sandbox)

	r := newReconcilerWithNetworking(t, sandbox, existingNP)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-sbx", Namespace: "default"}}

	_, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	npName := networkPolicyName("test-sbx")
	if _, ok := getNetworkPolicy(t, r, npName, "default"); ok {
		t.Errorf("expected NetworkPolicy to be deleted when IsolationPolicy changed to None, but it still exists")
	}
}

// TestNetworkPolicy_IsolationBetweenMultipleSandboxes verifies that each sandbox with
// Default isolation gets its own NetworkPolicy, and their selectors target only their
// respective launcher Pods.
func TestNetworkPolicy_IsolationBetweenMultipleSandboxes(t *testing.T) {
	sbx1 := newSandbox("sbx-alpha", "default")
	sbx1.Finalizers = []string{SandboxFinalizer}
	sbx1.Status.Phase = sandboxv1alpha1.SandboxPhasePending
	sbx1.Spec.NetworkPolicy = sandboxv1alpha1.NetworkPolicySpec{
		IsolationPolicy: sandboxv1alpha1.NetworkPolicyIsolationDefault,
	}

	sbx2 := newSandbox("sbx-beta", "default")
	sbx2.Finalizers = []string{SandboxFinalizer}
	sbx2.Status.Phase = sandboxv1alpha1.SandboxPhasePending
	sbx2.Spec.NetworkPolicy = sandboxv1alpha1.NetworkPolicySpec{
		IsolationPolicy: sandboxv1alpha1.NetworkPolicyIsolationDefault,
	}

	r := newReconcilerWithNetworking(t, sbx1, sbx2)

	// Reconcile sbx-alpha.
	req1 := ctrl.Request{NamespacedName: types.NamespacedName{Name: "sbx-alpha", Namespace: "default"}}
	if _, err := r.Reconcile(context.Background(), req1); err != nil {
		t.Fatalf("reconcile sbx-alpha error: %v", err)
	}

	// Reconcile sbx-beta.
	req2 := ctrl.Request{NamespacedName: types.NamespacedName{Name: "sbx-beta", Namespace: "default"}}
	if _, err := r.Reconcile(context.Background(), req2); err != nil {
		t.Fatalf("reconcile sbx-beta error: %v", err)
	}

	// Each sandbox should have its own NetworkPolicy.
	np1, ok := getNetworkPolicy(t, r, networkPolicyName("sbx-alpha"), "default")
	if !ok {
		t.Fatalf("expected NetworkPolicy for sbx-alpha to exist")
	}
	np2, ok := getNetworkPolicy(t, r, networkPolicyName("sbx-beta"), "default")
	if !ok {
		t.Fatalf("expected NetworkPolicy for sbx-beta to exist")
	}

	// np1 must select only sbx-alpha's launcher Pod.
	if np1.Spec.PodSelector.MatchLabels[LabelSandboxName] != "sbx-alpha" {
		t.Errorf("np1 PodSelector should target sbx-alpha, got %v", np1.Spec.PodSelector.MatchLabels)
	}
	// np2 must select only sbx-beta's launcher Pod.
	if np2.Spec.PodSelector.MatchLabels[LabelSandboxName] != "sbx-beta" {
		t.Errorf("np2 PodSelector should target sbx-beta, got %v", np2.Spec.PodSelector.MatchLabels)
	}

	// The ingress rules in np1 should reference sbx-alpha specifically for the same-sandbox rule.
	sameNameFound := false
	for _, rule := range np1.Spec.Ingress {
		for _, peer := range rule.From {
			if peer.PodSelector != nil {
				if peer.PodSelector.MatchLabels[LabelSandboxName] == "sbx-alpha" {
					sameNameFound = true
				}
			}
		}
	}
	if !sameNameFound {
		t.Errorf("np1 should contain an ingress rule allowing traffic from sbx-alpha's own pods")
	}
}

// TestNetworkPolicyName verifies the deterministic naming function.
func TestNetworkPolicyName(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"my-sandbox", "sbx-netpol-my-sandbox"},
		{"test", "sbx-netpol-test"},
	}
	for _, tc := range tests {
		got := networkPolicyName(tc.name)
		if got != tc.want {
			t.Errorf("networkPolicyName(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestBuildNetworkPolicy_Structure verifies the structure of the built NetworkPolicy.
func TestBuildNetworkPolicy_Structure(t *testing.T) {
	sandbox := &sandboxv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-sbx",
			Namespace: "staging",
			UID:       "uid-12345",
		},
	}
	np := buildNetworkPolicy(sandbox)

	if np.Name != "sbx-netpol-my-sbx" {
		t.Errorf("expected name sbx-netpol-my-sbx, got %s", np.Name)
	}
	if np.Namespace != "staging" {
		t.Errorf("expected namespace staging, got %s", np.Namespace)
	}
	if len(np.OwnerReferences) != 1 {
		t.Fatalf("expected 1 owner reference, got %d", len(np.OwnerReferences))
	}
	if np.OwnerReferences[0].Name != "my-sbx" {
		t.Errorf("expected owner reference name my-sbx, got %s", np.OwnerReferences[0].Name)
	}
	// Must only restrict Ingress, not Egress.
	if len(np.Spec.PolicyTypes) != 1 || np.Spec.PolicyTypes[0] != networkingv1.PolicyTypeIngress {
		t.Errorf("expected exactly Ingress policy type, got %v", np.Spec.PolicyTypes)
	}
}
