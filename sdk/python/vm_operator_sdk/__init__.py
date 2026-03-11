"""vm-operator-sdk – Python SDK for the vm-operator Kubernetes operator.

Quick start::

    import vm_operator_sdk as sdk

    # Authenticate via kubeconfig
    auth = sdk.from_kubeconfig()

    # Synchronous client
    with sdk.SandboxClient(auth) as client:
        sandbox = client.create_and_wait(
            "my-sandbox",
            template_id="ubuntu-22.04",
            vcpu=2,
            memory_mb=512,
        )
        print(sandbox.status.phase)   # SandboxPhase.RUNNING
        client.delete("my-sandbox")

    # Async client (requires aiohttp: pip install vm-operator-sdk[async])
    import asyncio

    async def run():
        async with sdk.AsyncSandboxClient(auth) as client:
            sandboxes = await client.create_batch(
                ["sb-1", "sb-2"], template_id="ubuntu-22.04"
            )

    asyncio.run(run())
"""

from ._version import __version__
from .async_client import AsyncSandboxClient
from .auth import AuthConfig, from_in_cluster, from_kubeconfig, from_token
from .client import SandboxClient
from .exceptions import (
    APIError,
    AuthenticationError,
    InvalidSpecError,
    PoolExhaustedError,
    SandboxAlreadyExistsError,
    SandboxNotFoundError,
    TimeoutError,
    VMOperatorError,
)
from .models import (
    IsolationPolicy,
    LifecycleSpec,
    NetworkPolicySpec,
    ResourcesSpec,
    RuntimeSpec,
    Sandbox,
    SandboxPhase,
    SandboxSpec,
    SandboxStatus,
    SchedulingSpec,
    TemplateSpec,
)
from .pool import AsyncSandboxPool, SandboxPool

__all__ = [
    # version
    "__version__",
    # auth
    "AuthConfig",
    "from_kubeconfig",
    "from_token",
    "from_in_cluster",
    # clients
    "SandboxClient",
    "AsyncSandboxClient",
    # pool managers
    "SandboxPool",
    "AsyncSandboxPool",
    # models
    "Sandbox",
    "SandboxSpec",
    "SandboxStatus",
    "SandboxPhase",
    "ResourcesSpec",
    "TemplateSpec",
    "LifecycleSpec",
    "SchedulingSpec",
    "NetworkPolicySpec",
    "RuntimeSpec",
    "IsolationPolicy",
    # exceptions
    "VMOperatorError",
    "AuthenticationError",
    "SandboxNotFoundError",
    "SandboxAlreadyExistsError",
    "APIError",
    "TimeoutError",
    "InvalidSpecError",
    "PoolExhaustedError",
]
