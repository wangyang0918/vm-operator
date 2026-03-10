# vm-operator

A Kubernetes Operator that manages Firecracker MicroVM-based sandbox environments, inspired by [E2B](https://github.com/e2b-dev/infra).

## Architecture

```
┌──────────────────────────────────────────────────────────────┐
│                       Kubernetes Cluster                      │
│                                                              │
│  ┌──────────────┐  CR apply   ┌──────────────────────────┐  │
│  │   User/API   │────────────▶│      vm-operator          │  │
│  └──────────────┘             │  (SandboxReconciler)      │  │
│                               └──────────┬───────────────┘  │
│                                          │ creates / deletes │
│                                          ▼                   │
│                              ┌──────────────────────┐       │
│                              │   launcher Pod        │       │
│                              │  (sbx-launcher-{name})│       │
│                              │  ┌─────────────────┐ │       │
│                              │  │ sandbox-launcher │ │       │
│                              │  │  (Firecracker VM │ │       │
│                              │  │   lifecycle mgr) │ │       │
│                              │  └─────────────────┘ │       │
│                              │  ┌─────────────────┐ │       │
│                              │  │  /dev/kvm mount  │ │       │
│                              │  └─────────────────┘ │       │
│                              └──────────────────────┘       │
└──────────────────────────────────────────────────────────────┘
```

The operator does **not** run the VM directly. Instead, for each `Sandbox` CR it creates a **launcher Pod** (similar to KubeVirt's `virt-launcher`). The Kubernetes scheduler places the Pod on a suitable node; the `sandbox-launcher` process inside that Pod manages the Firecracker MicroVM lifecycle.

### Operator ↔ Launcher Pod Relationship

| Concern | Operator (vm-operator) | Launcher Pod (sbx-launcher-*) |
|---|---|---|
| **Responsibility** | Watches Sandbox CRs; drives state machine | Runs and manages the Firecracker MicroVM process |
| **Scheduling** | Creates Pod with `nodeSelector` / `nodeName` constraints | Placed on a node by the native Kubernetes scheduler |
| **Resource enforcement** | Sets Pod CPU/memory Requests = Limits (see [Resource Isolation](#resource-isolation)) | Passes vCPU and memory values to Firecracker at boot |
| **Pause / Resume** | Annotates Pod with `sandbox.e2b.io/signal: pause`; deletes Pod after snapshot | On signal, saves VM snapshot; on resume, restores from snapshot |
| **Cleanup** | Deletes launcher Pod on termination; removes finalizer | Pod deleted by operator; Firecracker process exits |
| **Observability** | Exposes Prometheus metrics at `:8080/metrics` | Logs visible via `kubectl logs` on the launcher Pod |

### Isolation Model

Each Sandbox runs in its own dedicated Pod. Resource isolation is enforced at two levels:

1. **Pod level**: Kubernetes sets CPU and memory `Requests = Limits` on the container, preventing the launcher process from consuming more resources than allocated.
2. **VM level**: The `sandbox-launcher` passes the same vCPU count and memory size to Firecracker, so the guest OS sees exactly the resources specified in the `Sandbox.spec.resources` field.
3. **Network level**: When `spec.networkPolicy.isolationPolicy: Default` is set, a Kubernetes NetworkPolicy prevents other sandbox launcher Pods from sending traffic to this sandbox's launcher Pod.

The `/dev/kvm` device is bind-mounted into the launcher container via a `HostPath` volume. The launcher container currently uses `securityContext.privileged: true` to access this device. This is a known trade-off: future versions aim to restrict access to only `/dev/kvm` using device-plugin-based allocation or specific Linux capabilities, removing the need for full privileged mode.

## Feature Overview

### P0 – Core Sandbox Lifecycle

The core state machine handles the full lifecycle of a Firecracker MicroVM sandbox:

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

**Scheduling control** – Use `spec.scheduling.nodeSelector` or `spec.scheduling.nodeName` to pin sandboxes to specific nodes (e.g. bare-metal KVM hosts or nested-virtualisation nodes). The Kubernetes scheduler enforces these constraints natively.

**Timeout enforcement** – Set `spec.lifecycle.timeoutSeconds` (default 300 s). The operator transitions the sandbox to `Killing` as soon as the deadline passes.

### P1 – Pause / Resume and Metrics

**Pause / Resume** lets you snapshot a running MicroVM, release the node resources, and later restore the VM from the snapshot on the same or a different node.

Full pause/resume state machine:

```
Running ──► Pausing ──► Paused ──► Resuming ──► Initializing ──► Running
              │                        ▲
              │  (spec.paused=true)    │  (spec.paused=false)
              └────────────────────────┘
```

| Phase       | Trigger                              | Action                                      |
|-------------|--------------------------------------|---------------------------------------------|
| `Pausing`   | `spec.paused` set to `true`          | Annotate Pod → launcher saves snapshot; delete Pod |
| `Paused`    | Pod deleted, snapshot saved          | Stores `status.snapshotId`; releases node   |
| `Resuming`  | `spec.paused` cleared to `false`     | Creates new launcher Pod with `SNAPSHOT_ID` env var |

**Metrics (Prometheus)** – The operator registers and exposes 7 Prometheus metrics; see [Metrics](#metrics-prometheus).

### P3 – Webhook Validation ✅

The operator includes a `ValidatingWebhook` for the Sandbox CRD that enforces business rules at admission time, before any controller logic runs. See [Admission Webhook](#admission-webhook) for full details and setup instructions.

### P3 – Network Policy Per-Sandbox Isolation ✅

Each Sandbox can opt into network isolation by setting `spec.networkPolicy.isolationPolicy: Default`. When enabled, the operator creates a Kubernetes `NetworkPolicy` named `sbx-netpol-{sandbox-name}` that:

- Selects the sandbox's launcher Pod.
- Denies ingress traffic from other sandbox launcher Pods (preventing cross-sandbox communication).
- Allows ingress from all non-launcher Pods (e.g. metrics scrapers, API gateway).
- Allows all egress.

The NetworkPolicy is owned by the Sandbox CR and is garbage-collected automatically when the Sandbox is deleted. See [Network Policy Isolation](#network-policy-isolation) for configuration details.

### Roadmap (future)

- Horizontal Pod Autoscaler integration for launcher Pods
- **Python SDK** – pip-installable `vm-operator-sdk` package with sync/async clients for AI Agent and code-interpreter usage (HTTP RESTful API under the hood)
- **VM Warm Pool** – pre-started pool of MicroVMs for fast SDK allocation and lower response latency
- **CR-based pool allocation** – consume pooled VMs directly via a `SandboxPool` CustomResource

### P2 – Python SDK 🚧

A pip-installable `vm-operator-sdk` package is available under [`sdk/python/`](sdk/python/README.md).
It wraps the Kubernetes REST API and exposes a Pythonic interface suitable for AI Agent and
code-interpreter backends.

**Install:**
```bash
pip install vm-operator-sdk            # sync client (no extra deps)
pip install "vm-operator-sdk[async]"   # + aiohttp for async client
```

**Example:**
```python
import vm_operator_sdk as sdk

auth = sdk.from_kubeconfig()

with sdk.SandboxClient(auth) as client:
    sandbox = client.create_and_wait(
        "my-sandbox", template_id="ubuntu-22.04", vcpu=2, memory_mb=512
    )
    print(sandbox.status.phase)   # SandboxPhase.RUNNING
    client.delete("my-sandbox")
```

See [`sdk/python/README.md`](sdk/python/README.md) for the full API reference, async examples, batch operations and error-handling guide.

## Resource Isolation

The `spec.resources` fields in the Sandbox CR are translated **directly** into Pod container resource requests and limits. Because `Requests == Limits`, Kubernetes treats the container as a **Guaranteed** QoS class, which prevents it from being OOM-killed by other workloads:

| Sandbox field | Unit | Pod container field | Formula |
|---|---|---|---|
| `spec.resources.vcpu` | virtual cores | `resources.requests.cpu` / `resources.limits.cpu` | `vcpu × 1000m` |
| `spec.resources.memoryMB` | MiB | `resources.requests.memory` / `resources.limits.memory` | `memoryMB × 1 MiB` |

The same values are passed as environment variables (`VCPU`, `MEMORY_MB`) to the `sandbox-launcher` process, which forwards them to Firecracker. This ensures the guest OS cannot allocate more memory or CPU time than what is reserved on the host.

```
Sandbox spec.resources.vcpu = 2
   └─► Pod container resources.requests.cpu = 2000m
   └─► Pod container resources.limits.cpu   = 2000m
   └─► Firecracker --vcpu-count 2            (set by launcher from VCPU env var)

Sandbox spec.resources.memoryMB = 512
   └─► Pod container resources.requests.memory = 512Mi
   └─► Pod container resources.limits.memory   = 512Mi
   └─► Firecracker --mem-size-mib 512          (set by launcher from MEMORY_MB env var)
```

Enable `spec.resources.hugePages: true` to have the launcher configure Firecracker with huge-page-backed guest memory for reduced TLB pressure on memory-intensive workloads.

## Network Policy Isolation

By default, no Kubernetes `NetworkPolicy` is created for a Sandbox. All network traffic is unrestricted at the Kubernetes layer (Firecracker still provides hypervisor-level isolation).

When `spec.networkPolicy.isolationPolicy: Default` is set, the operator creates a `NetworkPolicy` named `sbx-netpol-{sandbox-name}` in the same namespace as the Sandbox. This policy:

- **Selects** only the launcher Pod for that specific sandbox (matched by `sandbox.e2b.io/sandbox-name`).
- **Allows ingress** from pods that are **not** other sandbox launcher Pods (i.e., services, monitoring, gateways).
- **Allows ingress** from the sandbox's own launcher Pod (same-sandbox traffic).
- **Denies ingress** from all other sandbox launcher Pods (those with `sandbox.e2b.io/role=launcher` and a different `sandbox.e2b.io/sandbox-name`).
- **Allows all egress** (no restrictions on outbound traffic).

The `NetworkPolicy` is owned by the Sandbox CR and is garbage-collected automatically when the Sandbox is deleted.

### Configuration

```yaml
apiVersion: sandbox.e2b.io/v1alpha1
kind: Sandbox
metadata:
  name: isolated-sandbox
  namespace: default
spec:
  template:
    templateID: "ubuntu-22.04"
  resources:
    vcpu: 2
    memoryMB: 512
  networkPolicy:
    isolationPolicy: Default   # Deny ingress from other sandbox launcher Pods
```

To disable isolation after it was enabled, set `isolationPolicy: None` (or remove the field). The operator will delete the existing `NetworkPolicy`.

### Isolation Values

| `isolationPolicy` | Behavior |
|---|---|
| `None` (default) | No `NetworkPolicy` is created; all traffic is allowed. |
| `Default` | Creates a `NetworkPolicy` denying ingress from other sandbox launcher Pods. |

> **Note**: Network policy enforcement requires a CNI plugin that supports `NetworkPolicy` (e.g., Calico, Cilium, Weave Net). If your cluster does not have a policy-enforcing CNI, the `NetworkPolicy` object is created but has no effect.



**Group / Version / Kind:** `sandbox.e2b.io/v1alpha1 / Sandbox`  
**Short name:** `sbx`  
**Scope:** Namespaced

### spec

| Field | Type | Default | Required | Description |
|---|---|---|---|---|
| `spec.template.templateID` | string | — | ✅ | Identifier of the base disk/root filesystem template |
| `spec.template.baseTemplateID` | string | — | | Optional parent template ID |
| `spec.resources.vcpu` | integer | `2` | | Virtual CPU count (1–64); has a server-side default of 2 |
| `spec.resources.memoryMB` | integer | `512` | | Guest memory in MiB (128–65536); has a server-side default of 512 |
| `spec.resources.diskMB` | integer | `2048` | | Root disk size in MiB |
| `spec.resources.hugePages` | bool | `false` | | Use huge-page-backed guest memory |
| `spec.runtime.kernelVersion` | string | — | | Linux kernel version to boot |
| `spec.runtime.firecrackerVersion` | string | — | | Firecracker binary version |
| `spec.lifecycle.timeoutSeconds` | integer | `300` | | Max sandbox lifetime; 0 = no timeout |
| `spec.scheduling.nodeSelector` | map[string]string | — | | Key-value node labels for scheduling |
| `spec.scheduling.nodeName` | string | — | | Pin to a specific node by name |
| `spec.networkPolicy.isolationPolicy` | string | `None` | | Network isolation mode: `None` (no policy) or `Default` (deny ingress from other sandboxes) |
| `spec.sandboxMetadata` | map[string]string | — | | Arbitrary user-defined metadata (owner, project, etc.) |
| `spec.paused` | bool | `false` | | Set to `true` to pause (snapshot) the sandbox; clear to resume |

### status

| Field | Type | Description |
|---|---|---|
| `status.phase` | string | Current lifecycle phase (see state machine) |
| `status.nodeName` | string | Node where the launcher Pod is running |
| `status.podName` | string | Name of the launcher Pod (`sbx-launcher-{name}`) |
| `status.startTime` | time | When the sandbox entered `Running` |
| `status.endTime` | time | Projected timeout deadline |
| `status.snapshotId` | string | VM snapshot ID; set when the sandbox is paused |
| `status.pausedAt` | time | When the sandbox entered `Paused` |
| `status.conditions` | []Condition | Standard Kubernetes conditions (`Ready`, `SnapshotReady`) |
| `status.observedGeneration` | integer | Last generation processed by the controller |

### Pause / Resume Example

```yaml
# 1. Create a running sandbox
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
  lifecycle:
    timeoutSeconds: 3600

# 2. Pause it (set spec.paused=true)
kubectl patch sandbox my-sandbox --type=merge -p '{"spec":{"paused":true}}'

# 3. Watch the phase transition:  Running → Pausing → Paused
kubectl get sbx my-sandbox -w

# 4. Resume it (clear spec.paused)
kubectl patch sandbox my-sandbox --type=merge -p '{"spec":{"paused":false}}'

# 5. Phase transitions:  Paused → Resuming → Initializing → Running
kubectl get sbx my-sandbox -w
```

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

If you use the [Prometheus Operator](https://github.com/prometheus-operator/prometheus-operator), create a `Service` and `ServiceMonitor` in the operator namespace:

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

# Error breakdown by reason
sum by (reason) (sandbox_operator_error_total)

# Total sandboxes ever successfully started
sum(sandbox_operator_launcher_success_total)
```

### Testing / Verifying Metrics

```bash
# Port-forward the operator metrics port
kubectl -n vm-operator-system port-forward deploy/vm-operator-controller-manager 8080:8080 &

# Fetch raw metrics
curl -s http://localhost:8080/metrics | grep sandbox_operator

# Check a specific metric
curl -s http://localhost:8080/metrics | grep 'sandbox_operator_sandboxes_by_phase'

# Run unit tests for metrics logic
make test
```

## Quick Start

### Prerequisites

- Go 1.22+
- `kubectl` configured to a Kubernetes cluster
- Nodes with `/dev/kvm` available — either:
  - **Bare-metal nodes** with Intel VT-x / AMD-V enabled in BIOS
  - **Nested-virtualisation nodes** (e.g. AWS `.metal` instances, GCP `--enable-nested-virtualization` VMs)

Label nodes that support KVM so sandboxes can be scheduled there:

```bash
kubectl label node <node-name> sandbox.e2b.io/firecracker=true
```

### Install CRD

```bash
kubectl apply -f config/crd/bases/sandbox.e2b.io_sandboxes.yaml
```

### Deploy the Operator

```bash
kubectl apply -f config/rbac/role.yaml
kubectl apply -f config/manager/manager.yaml

# Verify the operator pod is running
kubectl -n vm-operator-system get pods
```

### Run Locally (Out-of-Cluster)

```bash
# Install CRD first, then start the operator against the current kubeconfig context
make run
```

### Run Tests

```bash
# Run all unit and controller tests
make test

# Run with verbose output
go test ./... -v
```

## Example CR

### Basic Sandbox

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

### High-Memory Sandbox with HugePages

```yaml
apiVersion: sandbox.e2b.io/v1alpha1
kind: Sandbox
metadata:
  name: highmem-sandbox
  namespace: default
spec:
  template:
    templateID: "ubuntu-22.04"
  resources:
    vcpu: 4
    memoryMB: 8192
    diskMB: 20480
    hugePages: true
  lifecycle:
    timeoutSeconds: 7200
  scheduling:
    nodeSelector:
      sandbox.e2b.io/firecracker: "true"
```

### Node-Pinned Sandbox

```yaml
apiVersion: sandbox.e2b.io/v1alpha1
kind: Sandbox
metadata:
  name: pinned-sandbox
  namespace: default
spec:
  template:
    templateID: "ubuntu-22.04"
  resources:
    vcpu: 1
    memoryMB: 256
  scheduling:
    nodeName: "worker-node-01"
```

## Node Requirements

### Bare-Metal Nodes

Bare-metal nodes provide the best Firecracker performance:

- Intel VT-x or AMD-V must be enabled in BIOS/UEFI
- `/dev/kvm` must exist and be accessible (`ls -la /dev/kvm`)
- Kernel version ≥ 4.14 recommended for full KVM feature support

### Nested Virtualisation Nodes

Nested virtualisation is supported on several cloud providers:

| Provider | How to enable |
|---|---|
| AWS | Use `.metal` instance types (e.g. `m5.metal`) |
| GCP | Pass `--enable-nested-virtualization` flag when creating the VM |
| Azure | Use bare-metal or `Standard_D*v5` with nested-virt SKUs |
| On-prem KVM hosts | Enable `kvm-intel.nested=1` or `kvm-amd.nested=1` kernel module option |

Verify nested virtualisation is available:

```bash
# On the node
cat /sys/module/kvm_intel/parameters/nested   # should print Y or 1
cat /sys/module/kvm_amd/parameters/nested     # AMD equivalent
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

## FAQ

**Q: What is the relationship between a Sandbox CR and a Pod?**  
A: Each Sandbox CR results in exactly one launcher Pod (`sbx-launcher-{sandbox-name}`). The Pod is owned by the Sandbox (via an `ownerReference`), so it is garbage-collected automatically when the CR is deleted. While a sandbox is `Paused`, no Pod exists; a new Pod is created when the sandbox resumes.

**Q: Can multiple Sandboxes share the same node?**  
A: Yes. Multiple launcher Pods can run on the same node. Each Firecracker VM is fully isolated at the hypervisor level. Node capacity limits (CPU, memory) are enforced by the Kubernetes scheduler using the Pod resource requests.

**Q: What node types are supported?**  
A: Any node that exposes `/dev/kvm`. This includes bare-metal servers with hardware virtualisation enabled and cloud VMs with nested virtualisation. See [Node Requirements](#node-requirements).

**Q: Does the operator require privileged containers?**  
A: The launcher container currently runs with `securityContext.privileged: true` to access `/dev/kvm`. This grants broader host access than strictly necessary; a future improvement is to restrict it using a device plugin or specific Linux capabilities (`CAP_SYS_ADMIN`). The operator pod itself (`vm-operator-controller-manager`) does **not** require privileged access.

**Q: How is sandbox isolation enforced?**  
A: Isolation operates at four layers: (1) Firecracker hypervisor isolation between guest and host, (2) Kubernetes Pod isolation (cgroups, namespaces) between launcher processes, (3) Pod resource limits that prevent a single sandbox from consuming more CPU or memory than specified in `spec.resources`, and (4) optional Kubernetes NetworkPolicy (`spec.networkPolicy.isolationPolicy: Default`) that blocks ingress from other sandbox launcher Pods at the network level.

**Q: What happens when a sandbox times out?**  
A: The operator transitions the sandbox to `Killing`, deletes the launcher Pod, and removes the finalizer, which allows the CR to be garbage-collected. The timeout is configured via `spec.lifecycle.timeoutSeconds`.

**Q: How does pause/resume work?**  
A: Setting `spec.paused: true` signals the launcher Pod (via the `sandbox.e2b.io/signal: pause` annotation) to save a VM memory snapshot. The operator then deletes the Pod (freeing node resources). A snapshot ID is stored in `status.snapshotId`. When `spec.paused` is cleared, a new launcher Pod is created with `SNAPSHOT_ID` set, and the launcher restores the VM from the snapshot.

**Q: Is snapshot data persisted across node failures?**  
A: Snapshot durability depends on the `sandbox-launcher` implementation. The operator stores only the snapshot identifier (`status.snapshotId`) in the CR; the actual snapshot data must be stored on shared or distributed storage by the launcher. Recommended approaches include: S3-compatible object storage (e.g. AWS S3, MinIO), a `ReadWriteMany` PersistentVolume (e.g. NFS, CephFS), or a distributed block store (e.g. Longhorn). Without external storage, a snapshot can only be resumed on the same node that created it.

**Q: How do I monitor the operator?**  
A: Scrape `:8080/metrics` on the operator Pod. Seven custom Prometheus metrics cover phase distribution, runtime duration, launcher outcomes, pause/resume counts, and error rates. See [Metrics](#metrics-prometheus) for details and example queries.

**Q: Can I run this without Firecracker (for testing)?**  
A: The operator itself is not coupled to Firecracker. It creates launcher Pods with a configurable image (`LauncherImage` constant in `launcher_pod.go`). You can substitute a mock launcher image during development to test the state machine without requiring KVM.

## Admission Webhook

The operator ships a **ValidatingWebhook** for the `Sandbox` CRD that rejects requests violating business rules before they reach the controller.

### Validation rules

| Field | Rule |
|-------|------|
| `spec.template.templateID` | Required; must be non-empty |
| `spec.resources.vcpu` | Must be between **1** and **64** (inclusive) |
| `spec.resources.memoryMB` | Must be between **128** and **65536** MiB (inclusive) |
| `spec.resources.diskMB` | When provided (non-zero), must be at least **512** MiB |
| `spec.runtime.firecrackerVersion` | When provided, must be a valid version string (e.g. `v1.3.3`, `1.4.0-dev`) |
| `spec.runtime.kernelVersion` | When provided, must be a valid version string (e.g. `5.10.68`) |
| `spec.lifecycle.timeoutSeconds` | Must be **≥ 0** |
| `spec.paused` (pause request) | `paused: false → true` only allowed when phase is **Running** |
| `spec.paused` (resume request) | `paused: true → false` only allowed when phase is **Paused** or **Pausing** |

### Enabling the webhook

The webhook server is embedded in the operator manager binary. To activate it in a cluster:

1. **Install cert-manager** (required for TLS certificate provisioning):

   ```bash
   kubectl apply -f https://github.com/cert-manager/cert-manager/releases/latest/download/cert-manager.yaml
   kubectl -n cert-manager wait --for=condition=Available deployment --all --timeout=120s
   ```

2. **Apply the webhook Service and ValidatingWebhookConfiguration**:

   ```bash
   kubectl apply -f config/webhook/service.yaml
   kubectl apply -f config/webhook/manifests.yaml
   ```

   This creates:
   - A `Service` that routes port 443 → 9443 (the operator's webhook server port) in namespace `vm-operator-system`.
   - A `ValidatingWebhookConfiguration` that intercepts `CREATE` and `UPDATE` operations on `Sandbox` resources.
   - A cert-manager `Certificate` and self-signed `Issuer` that provision a TLS certificate for the server.

3. **Mount the TLS certificate** in the operator Deployment by patching `manager.yaml` to add the secret volume:

   ```yaml
   volumes:
     - name: webhook-cert
       secret:
         secretName: vm-operator-webhook-server-cert
   containers:
     - name: manager
       volumeMounts:
         - mountPath: /tmp/k8s-webhook-server/serving-certs
           name: webhook-cert
           readOnly: true
   ```

4. **Restart the operator**:

   ```bash
   kubectl -n vm-operator-system rollout restart deployment vm-operator-controller-manager
   ```

### Example validation cases

**✅ Accepted – valid Sandbox**:
```yaml
apiVersion: sandbox.e2b.io/v1alpha1
kind: Sandbox
metadata:
  name: my-sandbox
spec:
  template:
    templateID: tpl-001
  resources:
    vcpu: 2
    memoryMB: 512
    diskMB: 2048
  runtime:
    firecrackerVersion: v1.3.3
    kernelVersion: 5.10.68
  lifecycle:
    timeoutSeconds: 300
```

**❌ Rejected – vCPU out of range**:
```yaml
spec:
  resources:
    vcpu: 128   # must be between 1 and 64
    memoryMB: 512
```
Error: `spec.resources.vcpu: Invalid value: 128: must be between 1 and 64`

**❌ Rejected – missing templateID**:
```yaml
spec:
  template: {}   # templateID is required
  resources:
    vcpu: 2
    memoryMB: 512
```
Error: `spec.template.templateID: Required value: templateID is required`

**❌ Rejected – invalid version string**:
```yaml
spec:
  runtime:
    firecrackerVersion: "latest"   # not a valid version format
```
Error: `spec.runtime.firecrackerVersion: Invalid value: "latest": must be a valid version string (e.g. v1.3.3, 5.10.68)`

**❌ Rejected – pause from non-Running phase**:
```yaml
# The sandbox is currently in Pending phase; pausing is not allowed yet.
spec:
  paused: true
```
Error: `spec.paused: Forbidden: cannot pause sandbox in phase "Pending"; sandbox must be Running`

## Roadmap

- **P0** ✅ Core state machine: Pending → Scheduling → Initializing → Running → Killing
- **P1** ✅ Pause / Resume with VM snapshot support
- **P1** ✅ Sandbox metrics (Prometheus)
- **P3** ✅ Webhook validation for Sandbox spec
- **P3** ✅ Network policy per-sandbox isolation
- **P2** 🚧 Python SDK (`vm-operator-sdk`) – sync/async HTTP client for AI Agent and code-interpreter integration
- **P2** VM Warm Pool – pre-started MicroVM pool for fast SDK allocation and lower response latency
- **P2** CR-based pool allocation – `SandboxPool` CustomResource to consume pooled VMs declaratively
