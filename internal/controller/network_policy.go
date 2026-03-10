package controller

import (
	"context"
	"fmt"

	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	sandboxv1alpha1 "github.com/wangyang0918/vm-operator/api/v1alpha1"
)

// networkPolicyName returns the deterministic name for the NetworkPolicy of a Sandbox.
func networkPolicyName(sandboxName string) string {
	return fmt.Sprintf("sbx-netpol-%s", sandboxName)
}

// buildNetworkPolicy constructs the NetworkPolicy object for the given Sandbox.
// The policy selects the sandbox's launcher Pod and denies ingress from other sandbox
// launcher Pods (those carrying the role=launcher label but a different sandbox-name),
// while allowing all other ingress and all egress.
func buildNetworkPolicy(sandbox *sandboxv1alpha1.Sandbox) *networkingv1.NetworkPolicy {
	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      networkPolicyName(sandbox.Name),
			Namespace: sandbox.Namespace,
			Labels: map[string]string{
				LabelRole:        "network-policy",
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
		Spec: networkingv1.NetworkPolicySpec{
			// Select the launcher Pod for this sandbox.
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					LabelSandboxName: sandbox.Name,
				},
			},
			// Allow ingress only from pods that are NOT other sandbox launcher Pods.
			// A pod is another sandbox launcher if it carries the role=launcher label
			// with a different sandbox-name value. We express this as two separate
			// ingress rules whose union covers all allowed sources:
			//   1. Pods that do not have the launcher role at all.
			//   2. Pods that have the launcher role AND belong to this same sandbox.
			Ingress: []networkingv1.NetworkPolicyIngressRule{
				{
					// Allow traffic from non-launcher pods (e.g. metrics scrapers, services).
					From: []networkingv1.NetworkPolicyPeer{
						{
							PodSelector: &metav1.LabelSelector{
								MatchExpressions: []metav1.LabelSelectorRequirement{
									{
										Key:      LabelRole,
										Operator: metav1.LabelSelectorOpNotIn,
										Values:   []string{RoleLauncher},
									},
								},
							},
						},
					},
				},
				{
					// Allow traffic from the launcher Pod of this same sandbox.
					From: []networkingv1.NetworkPolicyPeer{
						{
							PodSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									LabelRole:        RoleLauncher,
									LabelSandboxName: sandbox.Name,
								},
							},
						},
					},
				},
			},
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeIngress,
			},
		},
	}
}

// ensureNetworkPolicy creates, updates, or deletes the NetworkPolicy for the given
// Sandbox according to its spec.networkPolicy.isolationPolicy setting.
func (r *SandboxReconciler) ensureNetworkPolicy(ctx context.Context, sandbox *sandboxv1alpha1.Sandbox) error {
	netpolName := networkPolicyName(sandbox.Name)
	existing := &networkingv1.NetworkPolicy{}
	getErr := r.Get(ctx, types.NamespacedName{Name: netpolName, Namespace: sandbox.Namespace}, existing)

	if sandbox.Spec.NetworkPolicy.IsolationPolicy != sandboxv1alpha1.NetworkPolicyIsolationDefault {
		// Isolation is disabled – delete any NetworkPolicy that may already exist.
		if apierrors.IsNotFound(getErr) {
			return nil
		}
		if getErr != nil {
			return getErr
		}
		if err := r.Delete(ctx, existing); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete network policy: %w", err)
		}
		return nil
	}

	// Isolation is enabled – create or update the NetworkPolicy.
	desired := buildNetworkPolicy(sandbox)

	if apierrors.IsNotFound(getErr) {
		if err := r.Create(ctx, desired); err != nil {
			return fmt.Errorf("failed to create network policy: %w", err)
		}
		return nil
	}
	if getErr != nil {
		return getErr
	}

	// Update the spec of the existing NetworkPolicy in case sandbox labels changed.
	existing.Spec = desired.Spec
	if err := r.Update(ctx, existing); err != nil {
		return fmt.Errorf("failed to update network policy: %w", err)
	}
	return nil
}
