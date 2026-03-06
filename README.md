# vm-operator

A Kubernetes Operator that manages Firecracker MicroVM-based sandbox environments, inspired by [E2B](https://github.com/e2b-dev/infra).

## Architecture

```
┌──────────────────────────────────────────────────────────┐
│                     Kubernetes Cluster                    │
│                                                          │
│  ┌──────────────┐        ┌─────────────────────────────┐ │
│  │   User/API   │  CR    │      vm-operator             │ │
│  │              │───────▶│  (SandboxReconciler)         │ │
│  └──────────────┘        └───────────┬─────────────────┘ │
│                                      │ creates            │
│                                      ▼                    │
│                          ┌──────────────────────┐        │
│                          │   launcher Pod        │        │
│                          │  (sbx-launcher-{name})│        │
│                          │  ┌─────────────────┐ │        │
│                          │  │ sandbox-launcher │ │        │
│                          │  │  (manages        │ │        │
│                          │  │  Firecracker VM) │ │        │
│                          │  └─────────────────┘ │        │
│                          └──────────────────────┘        │
└──────────────────────────────────────────────────────────┘
```

The operator does **not** run the VM directly. Instead, for each `Sandbox` CR it creates a **launcher Pod** (similar to KubeVirt's `virt-launcher`). The Pod is scheduled by Kubernetes' native scheduler, and the `sandbox-launcher` process inside the Pod manages the Firecracker MicroVM lifecycle.

## P0 State Machine

```
New CR ──► Pending ──► Scheduling ──► Initializing ──► Running
                          │                               │
                          │ (Unschedulable)               │ (timeout / Pod gone)
                          ▼                               ▼
                        Failed                         Killing ──► (finalizer removed)
                                                          ▲
                                              DeletionTimestamp set (any phase)
```

| Phase          | Trigger                               | Requeue    |
|----------------|---------------------------------------|------------|
| `Pending`      | Finalizer added to CR                 | immediate  |
| `Scheduling`   | Launcher Pod created                  | 5 s        |
| `Initializing` | Pod assigned to a node                | 3 s        |
| `Running`      | Pod phase = Running                   | 30 s       |
| `Killing`      | Timeout OR DeletionTimestamp set      | immediate  |
| `Failed`       | Pod Unschedulable OR Pod Failed       | —          |

## Quick Start

### Prerequisites

- Go 1.22+
- kubectl configured to a Kubernetes cluster
- Nodes with `/dev/kvm` available (for Firecracker)

### Install CRD

```bash
kubectl apply -f config/crd/bases/sandbox.e2b.io_sandboxes.yaml
```

### Deploy the operator

```bash
kubectl apply -f config/rbac/role.yaml
kubectl apply -f config/manager/manager.yaml
```

### Run locally (out-of-cluster)

```bash
make run
```

## Example CR

```yaml
apiVersion: sandbox.e2b.io/v1alpha1
kind: Sandbox
metadata:
  name: my-sandbox
  namespace: default
spec:
  template:
    templateID: "ubuntu-22.04"
  resources:
    vcpu: 2
    memoryMB: 512
    diskMB: 4096
  lifecycle:
    timeoutSeconds: 600
  runtime:
    kernelVersion: "5.10.186"
    firecrackerVersion: "1.7.0"
  scheduling:
    nodeSelector:
      sandbox.e2b.io/firecracker: "true"
  sandboxMetadata:
    owner: "alice"
    project: "ml-experiment"
```

## Running Tests

```bash
make test
```

## Project Structure

```
vm-operator/
├── api/v1alpha1/
│   ├── sandbox_types.go          # CRD type definitions
│   ├── groupversion_info.go      # Group/version registration
│   └── zz_generated.deepcopy.go # Generated deepcopy methods
├── cmd/main.go                   # Operator entrypoint
├── config/
│   ├── crd/bases/                # CRD YAML manifests
│   ├── rbac/role.yaml            # RBAC ClusterRole
│   └── manager/manager.yaml     # Deployment manifests
├── internal/controller/
│   ├── sandbox_reconciler.go    # State machine reconciler
│   ├── launcher_pod.go          # Launcher Pod builder
│   └── sandbox_reconciler_test.go
├── go.mod
├── Makefile
└── README.md
```

## Roadmap

- **P0** ✅ Core state machine: Pending → Scheduling → Initializing → Running → Killing
- **P1** Sandbox metrics (Prometheus)
- **P1** Webhook validation for Sandbox spec
- **P2** Multi-cluster support
- **P2** Snapshot/restore support for MicroVM state
