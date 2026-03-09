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

## Metrics (Prometheus)

The operator exposes Prometheus metrics on **`:8080/metrics`** (configurable via `--metrics-bind-address`). No sidecar is required — the metrics server is embedded in the operator process via controller-runtime.

### Exported Metrics

| Metric | Type | Labels | Description |
|---|---|---|---|
| `sandbox_operator_sandboxes_by_phase` | Gauge | `phase` | Number of sandboxes currently in each lifecycle phase (`Pending`, `Scheduling`, `Initializing`, `Running`, `Pausing`, `Paused`, `Resuming`, `Killing`, `Failed`). |
| `sandbox_operator_sandbox_runtime_seconds` | Gauge | `sandbox`, `namespace` | Seconds elapsed since a sandbox entered the Running state. Cleared when the sandbox leaves Running. |
| `sandbox_operator_launcher_success_total` | Counter | `namespace` | Total launcher pods that successfully reached the Running state. |
| `sandbox_operator_launcher_failure_total` | Counter | `namespace` | Total launcher pods that failed to start (Unschedulable or PodFailed). |
| `sandbox_operator_pause_total` | Counter | `namespace` | Total sandbox pause operations. |
| `sandbox_operator_resume_total` | Counter | `namespace` | Total sandbox resume operations. |
| `sandbox_operator_error_total` | Counter | `namespace`, `reason` | Total transitions to the Failed phase, labelled by failure reason (e.g. `PodFailed`, `Unschedulable`, `PodMissing`). |

Controller-runtime also exposes standard Go runtime, process, and controller reconciliation metrics at the same endpoint.

### Prometheus Scrape Configuration

```yaml
scrape_configs:
  - job_name: vm-operator
    static_configs:
      - targets: ["<operator-pod-ip>:8080"]
```

### Prometheus Operator ServiceMonitor

If you use the [Prometheus Operator](https://github.com/prometheus-operator/prometheus-operator), create a `ServiceMonitor` alongside a `Service` that exposes the metrics port:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: vm-operator-metrics
  namespace: vm-operator-system
  labels:
    app: vm-operator
spec:
  ports:
    - name: metrics
      port: 8080
      targetPort: 8080
  selector:
    app: vm-operator
---
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: vm-operator
  namespace: vm-operator-system
spec:
  selector:
    matchLabels:
      app: vm-operator
  endpoints:
    - port: metrics
      path: /metrics
      interval: 30s
```

### Example Queries

```promql
# Sandboxes currently running
sandbox_operator_sandboxes_by_phase{phase="Running"}

# Launcher failure rate over the last 5 minutes
rate(sandbox_operator_launcher_failure_total[5m])

# Average sandbox runtime
avg(sandbox_operator_sandbox_runtime_seconds)

# Pause operations per namespace
sum by (namespace) (sandbox_operator_pause_total)
```

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
│   ├── metrics.go               # Prometheus metrics registration & helpers
│   ├── sandbox_reconciler_test.go
│   └── metrics_test.go
├── go.mod
├── Makefile
└── README.md
```

## Roadmap

- **P0** ✅ Core state machine: Pending → Scheduling → Initializing → Running → Killing
- **P1** ✅ Sandbox metrics (Prometheus)
- **P1** Webhook validation for Sandbox spec
- **P2** Multi-cluster support
- **P2** Snapshot/restore support for MicroVM state
