# vm-operator-sdk

Python SDK for the [vm-operator](https://github.com/wangyang0918/vm-operator) Kubernetes operator, which manages Firecracker MicroVM sandbox environments.

The SDK provides a high-level Python interface (synchronous **and** asynchronous) for creating, querying, pausing, resuming and deleting Sandbox resources. Internally it talks to the Kubernetes REST API, making it easy to integrate vm-operator into AI Agent pipelines, code-interpreter backends, and automated testing workflows.

## Installation

```bash
# Core install (sync client only, no extra runtime dependencies)
pip install vm-operator-sdk

# With async support (installs aiohttp)
pip install "vm-operator-sdk[async]"

# With kubeconfig support (installs PyYAML)
pip install "vm-operator-sdk[kubeconfig]"

# Everything
pip install "vm-operator-sdk[async,kubeconfig]"
```

## Quick Start

### Authentication

Three authentication modes are supported:

```python
import vm_operator_sdk as sdk

# 1. Kubeconfig (reads ~/.kube/config or $KUBECONFIG)
auth = sdk.from_kubeconfig()

# 2. Bearer token (e.g. service-account JWT)
auth = sdk.from_token(
    server="https://k8s.example.com:6443",
    token="eyJhbGci...",
    ca_bundle_file="/path/to/ca.crt",
)

# 3. In-cluster (when running inside a Kubernetes Pod)
auth = sdk.from_in_cluster()
```

### Synchronous Client

```python
import vm_operator_sdk as sdk

auth = sdk.from_kubeconfig()

with sdk.SandboxClient(auth) as client:
    # Create a sandbox and wait until it is Running
    sandbox = client.create_and_wait(
        "my-sandbox",
        template_id="ubuntu-22.04",
        vcpu=2,
        memory_mb=512,
        timeout_seconds=300,
    )
    print(sandbox.status.phase)   # SandboxPhase.RUNNING
    print(sandbox.status.node_name)

    # Retrieve a sandbox by name
    sandbox = client.get("my-sandbox")

    # List all sandboxes in a namespace
    sandboxes = client.list(namespace="default")

    # Pause (snapshot) a running sandbox
    client.pause("my-sandbox")

    # Resume a paused sandbox
    client.resume("my-sandbox")

    # Delete when done
    client.delete("my-sandbox")
```

### Asynchronous Client

```python
import asyncio
import vm_operator_sdk as sdk

auth = sdk.from_kubeconfig()

async def main():
    async with sdk.AsyncSandboxClient(auth) as client:
        # Create multiple sandboxes concurrently
        sandboxes = await client.create_batch(
            ["sb-1", "sb-2", "sb-3"],
            template_id="ubuntu-22.04",
            vcpu=2,
            memory_mb=512,
        )

        # Wait for each to become Running
        running = await asyncio.gather(
            *[client.wait_until_running(sb.name) for sb in sandboxes]
        )

        # Clean up
        await client.delete_batch([sb.name for sb in running])

asyncio.run(main())
```

### Batch Operations

```python
# Create many sandboxes at once (sync)
with sdk.SandboxClient(auth) as client:
    sandboxes = client.create_batch(
        ["worker-1", "worker-2", "worker-3"],
        template_id="ubuntu-22.04",
        vcpu=4,
        memory_mb=1024,
    )
    client.delete_batch([sb.name for sb in sandboxes])
```

### Network Isolation

```python
import vm_operator_sdk as sdk

with sdk.SandboxClient(sdk.from_kubeconfig()) as client:
    sandbox = client.create(
        "isolated-sandbox",
        template_id="ubuntu-22.04",
        isolation_policy=sdk.IsolationPolicy.DEFAULT,  # deny cross-sandbox ingress
    )
```

### Advanced Scheduling

```python
with sdk.SandboxClient(sdk.from_kubeconfig()) as client:
    # Pin to a specific node
    sandbox = client.create(
        "pinned-sandbox",
        template_id="ubuntu-22.04",
        node_name="bare-metal-kvm-node-1",
    )

    # Use a node selector
    sandbox = client.create(
        "kvm-sandbox",
        template_id="ubuntu-22.04",
        node_selector={"kvm": "true", "region": "us-west-2"},
    )
```

### Error Handling

```python
import vm_operator_sdk as sdk

auth = sdk.from_kubeconfig()

try:
    with sdk.SandboxClient(auth) as client:
        sandbox = client.create_and_wait("my-sandbox", template_id="ubuntu-22.04")
except sdk.SandboxAlreadyExistsError as e:
    print(f"Sandbox already exists: {e.name}/{e.namespace}")
except sdk.SandboxNotFoundError as e:
    print(f"Sandbox not found: {e.name}/{e.namespace}")
except sdk.TimeoutError:
    print("Sandbox did not become Running in time")
except sdk.APIError as e:
    print(f"Kubernetes API error {e.status_code}: {e.message}")
except sdk.VMOperatorError as e:
    print(f"SDK error: {e}")
```

## VM Pooling

The SDK ships with a built-in pool manager that solves a key limitation of
talking directly to the Kubernetes APIServer: because the APIServer has no concept
of "idle" vs "in-use" VMs, every `create` call spins up a brand-new MicroVM and
waits for it to boot – which can take tens of seconds.

`SandboxPool` (sync) and `AsyncSandboxPool` (async) pre-create a set of
Sandboxes and track their state with Kubernetes labels:

| Label | Values |
|---|---|
| `vm-operator/pool` | pool name – identifies pool members |
| `vm-operator/pool-state` | `idle` (available) or `in-use` (checked out) |

When a caller calls `acquire()`, the pool lists all Sandboxes that are
`idle` **and** in `Running` phase, claims the first one (by patching the
label to `in-use`), and returns it immediately – no boot wait.  When done,
`release()` patches the label back to `idle` so the next caller can use it.

### Synchronous pool

```python
import vm_operator_sdk as sdk

auth = sdk.from_kubeconfig()

with sdk.SandboxClient(auth) as client:
    pool = sdk.SandboxPool(
        client,
        pool_name="my-pool",   # unique identifier; used as a Kubernetes label value
        size=5,                # target number of idle VMs
        template_id="ubuntu-22.04",
        vcpu=2,
        memory_mb=512,
    )

    # Pre-warm the pool: create 5 idle VMs and wait until all are Running
    pool.replenish()

    # Acquire a running VM instantly (no boot latency)
    with pool.acquire_context() as sandbox:
        print(sandbox.name, sandbox.phase)  # SandboxPhase.RUNNING
        # ... submit work to the sandbox ...
    # sandbox is returned to the pool automatically

    # Inspect pool state
    print(len(pool.idle_sandboxes()))   # VMs available for acquisition
    print(len(pool.in_use_sandboxes())) # VMs currently checked out

    # Shut down the pool when no longer needed
    pool.drain()
```

### Asynchronous pool

```python
import asyncio
import vm_operator_sdk as sdk

async def main():
    auth = sdk.from_kubeconfig()
    async with sdk.AsyncSandboxClient(auth) as client:
        pool = sdk.AsyncSandboxPool(
            client,
            pool_name="async-pool",
            size=3,
            template_id="ubuntu-22.04",
        )

        await pool.replenish()          # pre-warm concurrently

        async with pool.acquire_context() as sandbox:
            print(sandbox.name)
            # ... use sandbox ...

        await pool.drain()

asyncio.run(main())
```

### Manual acquire / release

```python
# Without the context-manager helper
sandbox = pool.acquire()
try:
    # ... use sandbox ...
finally:
    pool.release(sandbox.name)
```

## AI Agent / Code-Interpreter Integration

The SDK is designed to work seamlessly in notebook and AI agent environments:

```python
# Example: use inside a code-interpreter agent step
import vm_operator_sdk as sdk

auth = sdk.from_in_cluster()  # running inside Kubernetes

with sdk.SandboxClient(auth, default_namespace="agents") as client:
    # Spin up a sandbox for code execution
    sandbox = client.create_and_wait(
        "agent-run-42",
        template_id="python-3.11",
        vcpu=2,
        memory_mb=1024,
        timeout_seconds=600,
        sandbox_metadata={"agent-id": "42", "task": "data-analysis"},
    )

    # ... submit code to the running sandbox via its launcher Pod ...

    # Tear down when finished
    client.delete(sandbox.name)
```

## Data Models

| Class | Description |
|---|---|
| `Sandbox` | Top-level resource: name, namespace, spec, status |
| `SandboxSpec` | Desired state: resources, template, lifecycle, scheduling, network policy |
| `SandboxStatus` | Observed state: phase, nodeName, podName, snapshotId, conditions |
| `ResourcesSpec` | vCPU, memory (MiB), disk (MiB), huge pages |
| `TemplateSpec` | Template ID and optional base template ID |
| `LifecycleSpec` | Timeout in seconds |
| `SchedulingSpec` | Node selector / node name constraints |
| `NetworkPolicySpec` | Isolation policy (`None` or `Default`) |
| `SandboxPhase` | Enum: Pending, Scheduling, Initializing, Running, Pausing, Paused, Resuming, Killing, Failed |
| `IsolationPolicy` | Enum: `None`, `Default` |

## Pool Classes

| Class | Description |
|---|---|
| `SandboxPool` | Synchronous pool of pre-created idle Sandbox VMs |
| `AsyncSandboxPool` | Asynchronous pool of pre-created idle Sandbox VMs |

## Exceptions

| Exception | When raised |
|---|---|
| `VMOperatorError` | Base class for all SDK errors |
| `AuthenticationError` | Invalid or missing auth configuration |
| `SandboxNotFoundError` | Sandbox does not exist (`.name`, `.namespace` attributes) |
| `SandboxAlreadyExistsError` | Sandbox already exists (`.name`, `.namespace` attributes) |
| `APIError` | Unexpected Kubernetes API HTTP error (`.status_code`, `.message`) |
| `TimeoutError` | `wait_until_running` timed out |
| `InvalidSpecError` | Invalid Sandbox specification |
| `PoolExhaustedError` | No idle Sandbox available in a pool within `acquire_timeout` (`.pool_name`) |

## Development

```bash
# Clone the repository
git clone https://github.com/wangyang0918/vm-operator.git
cd vm-operator/sdk/python

# Install in editable mode with dev dependencies
pip install -e ".[dev]"

# Run tests
pytest tests/ -v

# Run tests with coverage
pytest tests/ -v --cov=vm_operator_sdk
```

## License

Apache 2.0 – see the repository root `LICENSE` file.
