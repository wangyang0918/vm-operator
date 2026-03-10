"""Asynchronous HTTP client for the vm-operator Sandbox API.

Provides the same interface as :class:`~vm_operator_sdk.client.SandboxClient`
but uses ``asyncio`` throughout.  Requires Python 3.8+ and the ``aiohttp``
library (installed automatically via the ``[async]`` extra).

Example::

    import asyncio
    import vm_operator_sdk as sdk

    async def main():
        auth = sdk.from_kubeconfig()
        async with sdk.AsyncSandboxClient(auth) as client:
            sandbox = await client.create_and_wait(
                "my-sandbox", template_id="ubuntu-22.04"
            )
            print(sandbox.status.phase)
            await client.delete("my-sandbox")

    asyncio.run(main())
"""

from __future__ import annotations

import asyncio
import json
import ssl
from typing import Dict, List, Optional, Union

from .auth import AuthConfig
from .exceptions import (
    APIError,
    SandboxAlreadyExistsError,
    SandboxNotFoundError,
    TimeoutError,
)
from .models import (
    IsolationPolicy,
    LifecycleSpec,
    NetworkPolicySpec,
    ResourcesSpec,
    Sandbox,
    SandboxPhase,
    SandboxSpec,
    SchedulingSpec,
    TemplateSpec,
)

_GROUP = "sandbox.e2b.io"
_VERSION = "v1alpha1"
_PLURAL = "sandboxes"


class AsyncSandboxClient:
    """Asynchronous client for managing Sandbox resources.

    Parameters
    ----------
    auth:
        Authentication configuration.
    default_namespace:
        Namespace used when no explicit namespace is supplied.
    """

    def __init__(
        self,
        auth: AuthConfig,
        *,
        default_namespace: str = "default",
    ) -> None:
        self._auth = auth
        self._default_namespace = default_namespace
        self._session = None

    # ------------------------------------------------------------------
    # Session lifecycle
    # ------------------------------------------------------------------

    async def _get_session(self):
        """Return a live aiohttp ClientSession, creating one if necessary."""
        try:
            import aiohttp
        except ImportError as exc:
            raise ImportError(
                "aiohttp is required for AsyncSandboxClient. "
                "Install it with: pip install vm-operator-sdk[async]"
            ) from exc

        if self._session is None or self._session.closed:
            ssl_ctx: Union[ssl.SSLContext, bool]
            if self._auth.insecure_skip_tls_verify:
                ssl_ctx = False
            else:
                ssl_ctx = self._auth.ssl_context() or True

            connector = aiohttp.TCPConnector(ssl=ssl_ctx)
            self._session = aiohttp.ClientSession(
                headers=self._auth.request_headers(),
                connector=connector,
            )
        return self._session

    async def close(self) -> None:
        """Close the underlying HTTP session and clean up resources."""
        if self._session and not self._session.closed:
            await self._session.close()
        self._auth.cleanup()

    # ------------------------------------------------------------------
    # Internal helpers
    # ------------------------------------------------------------------

    def _url(self, namespace: str, name: str = "") -> str:
        base = (
            f"{self._auth.server}/apis/{_GROUP}/{_VERSION}"
            f"/namespaces/{namespace}/{_PLURAL}"
        )
        return f"{base}/{name}" if name else base

    async def _request(
        self,
        method: str,
        url: str,
        body: Optional[dict] = None,
        content_type: str = "application/json",
    ) -> dict:
        """Execute an async HTTP request and return the parsed JSON response."""
        import aiohttp

        session = await self._get_session()
        headers = {"Content-Type": content_type}
        data: Optional[str] = json.dumps(body) if body is not None else None

        async with session.request(
            method, url, data=data, headers=headers
        ) as resp:
            text = await resp.text()
            if resp.status == 404:
                raise SandboxNotFoundError("", "")
            if resp.status == 409:
                raise SandboxAlreadyExistsError("", "")
            if not (200 <= resp.status < 300):
                try:
                    msg = json.loads(text).get("message", text)
                except (json.JSONDecodeError, AttributeError):
                    msg = text
                raise APIError(resp.status, msg)
            return json.loads(text)

    # ------------------------------------------------------------------
    # CRUD operations
    # ------------------------------------------------------------------

    async def create(
        self,
        name: str,
        *,
        namespace: Optional[str] = None,
        template_id: str = "",
        vcpu: int = 2,
        memory_mb: int = 512,
        disk_mb: int = 2048,
        timeout_seconds: int = 300,
        node_selector: Optional[Dict[str, str]] = None,
        node_name: str = "",
        isolation_policy: IsolationPolicy = IsolationPolicy.NONE,
        sandbox_metadata: Optional[Dict[str, str]] = None,
        labels: Optional[Dict[str, str]] = None,
        annotations: Optional[Dict[str, str]] = None,
    ) -> Sandbox:
        """Create a new Sandbox.  See :meth:`~vm_operator_sdk.client.SandboxClient.create`."""
        ns = namespace or self._default_namespace
        spec = SandboxSpec(
            resources=ResourcesSpec(vcpu=vcpu, memory_mb=memory_mb, disk_mb=disk_mb),
            template=TemplateSpec(template_id=template_id),
            lifecycle=LifecycleSpec(timeout_seconds=timeout_seconds),
            scheduling=SchedulingSpec(
                node_selector=node_selector or {}, node_name=node_name
            ),
            network_policy=NetworkPolicySpec(isolation_policy=isolation_policy),
            sandbox_metadata=sandbox_metadata or {},
        )
        sandbox = Sandbox(
            name=name,
            namespace=ns,
            spec=spec,
            labels=labels or {},
            annotations=annotations or {},
        )
        manifest = sandbox.to_manifest()
        try:
            raw = await self._request("POST", self._url(ns), body=manifest)
        except SandboxAlreadyExistsError:
            raise SandboxAlreadyExistsError(name, ns)
        return Sandbox.from_manifest(raw)

    async def get(self, name: str, *, namespace: Optional[str] = None) -> Sandbox:
        """Retrieve a Sandbox by name."""
        ns = namespace or self._default_namespace
        try:
            raw = await self._request("GET", self._url(ns, name))
        except SandboxNotFoundError:
            raise SandboxNotFoundError(name, ns)
        return Sandbox.from_manifest(raw)

    async def list(self, *, namespace: Optional[str] = None) -> List[Sandbox]:
        """Return all Sandboxes in *namespace*."""
        ns = namespace or self._default_namespace
        raw = await self._request("GET", self._url(ns))
        return [Sandbox.from_manifest(item) for item in raw.get("items", [])]

    async def delete(self, name: str, *, namespace: Optional[str] = None) -> None:
        """Delete a Sandbox."""
        ns = namespace or self._default_namespace
        try:
            await self._request("DELETE", self._url(ns, name))
        except SandboxNotFoundError:
            raise SandboxNotFoundError(name, ns)

    async def pause(self, name: str, *, namespace: Optional[str] = None) -> Sandbox:
        """Pause a running Sandbox."""
        ns = namespace or self._default_namespace
        patch = {"spec": {"paused": True}}
        try:
            raw = await self._request(
                "PATCH",
                self._url(ns, name),
                body=patch,
                content_type="application/merge-patch+json",
            )
        except SandboxNotFoundError:
            raise SandboxNotFoundError(name, ns)
        return Sandbox.from_manifest(raw)

    async def resume(self, name: str, *, namespace: Optional[str] = None) -> Sandbox:
        """Resume a paused Sandbox."""
        ns = namespace or self._default_namespace
        patch = {"spec": {"paused": False}}
        try:
            raw = await self._request(
                "PATCH",
                self._url(ns, name),
                body=patch,
                content_type="application/merge-patch+json",
            )
        except SandboxNotFoundError:
            raise SandboxNotFoundError(name, ns)
        return Sandbox.from_manifest(raw)

    async def patch_labels(
        self,
        name: str,
        labels: Dict[str, str],
        *,
        namespace: Optional[str] = None,
    ) -> Sandbox:
        """Merge-patch the Kubernetes labels on a Sandbox.

        Parameters
        ----------
        name:
            Name of the Sandbox to patch.
        labels:
            Labels to set (merged with the existing label map).
        namespace:
            Namespace of the Sandbox.  Defaults to ``default_namespace``.

        Returns
        -------
        Sandbox
            The updated Sandbox as returned by the API server.

        Raises
        ------
        SandboxNotFoundError
            If the Sandbox does not exist.
        """
        ns = namespace or self._default_namespace
        patch = {"metadata": {"labels": labels}}
        try:
            raw = await self._request(
                "PATCH",
                self._url(ns, name),
                body=patch,
                content_type="application/merge-patch+json",
            )
        except SandboxNotFoundError:
            raise SandboxNotFoundError(name, ns)
        return Sandbox.from_manifest(raw)

    # ------------------------------------------------------------------
    # Convenience / high-level operations
    # ------------------------------------------------------------------

    async def wait_until_running(
        self,
        name: str,
        *,
        namespace: Optional[str] = None,
        timeout: float = 120.0,
        poll_interval: float = 2.0,
    ) -> Sandbox:
        """Async-poll until the Sandbox reaches the ``Running`` phase."""
        ns = namespace or self._default_namespace
        deadline = asyncio.get_event_loop().time() + timeout
        while asyncio.get_event_loop().time() < deadline:
            sandbox = await self.get(name, namespace=ns)
            if sandbox.phase == SandboxPhase.RUNNING:
                return sandbox
            if sandbox.status and sandbox.status.is_terminal:
                raise APIError(
                    0,
                    f"Sandbox '{name}' entered terminal phase: {sandbox.phase}",
                )
            await asyncio.sleep(poll_interval)
        raise TimeoutError(
            f"Sandbox '{name}' did not reach Running within {timeout}s"
        )

    async def create_and_wait(
        self,
        name: str,
        *,
        namespace: Optional[str] = None,
        timeout: float = 120.0,
        **create_kwargs,
    ) -> Sandbox:
        """Create a Sandbox and await until it is Running."""
        ns = namespace or self._default_namespace
        await self.create(name, namespace=ns, **create_kwargs)
        return await self.wait_until_running(name, namespace=ns, timeout=timeout)

    async def create_batch(
        self,
        names: List[str],
        *,
        namespace: Optional[str] = None,
        **create_kwargs,
    ) -> List[Sandbox]:
        """Create multiple Sandboxes concurrently."""
        ns = namespace or self._default_namespace
        tasks = [
            asyncio.create_task(self.create(n, namespace=ns, **create_kwargs))
            for n in names
        ]
        return list(await asyncio.gather(*tasks))

    async def delete_batch(
        self,
        names: List[str],
        *,
        namespace: Optional[str] = None,
        ignore_not_found: bool = True,
    ) -> None:
        """Delete multiple Sandboxes concurrently."""
        ns = namespace or self._default_namespace

        async def _delete_one(n: str) -> None:
            try:
                await self.delete(n, namespace=ns)
            except SandboxNotFoundError:
                if not ignore_not_found:
                    raise

        await asyncio.gather(*[_delete_one(n) for n in names])

    # ------------------------------------------------------------------
    # Context manager support
    # ------------------------------------------------------------------

    async def __aenter__(self) -> "AsyncSandboxClient":
        return self

    async def __aexit__(self, *_) -> None:
        await self.close()
