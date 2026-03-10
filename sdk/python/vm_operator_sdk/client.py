"""Synchronous HTTP client for the vm-operator Sandbox API.

The client wraps the Kubernetes Custom Resource REST API for the
``sandbox.e2b.io/v1alpha1/Sandbox`` resource and exposes a Pythonic interface
suitable for use in AI Agent / code-interpreter workflows.

Example::

    import vm_operator_sdk as sdk

    auth = sdk.from_kubeconfig()
    client = sdk.SandboxClient(auth)

    sandbox = client.create("my-sandbox", template_id="ubuntu-22.04", vcpu=2, memory_mb=512)
    sandbox = client.wait_until_running(sandbox.name, sandbox.namespace)
    print(sandbox.status.phase)
    client.delete("my-sandbox")
"""

from __future__ import annotations

import json
import time
import urllib.request
import urllib.error
from typing import Dict, Iterator, List, Optional

from .auth import AuthConfig
from .exceptions import (
    APIError,
    SandboxAlreadyExistsError,
    SandboxNotFoundError,
    TimeoutError,
)
from .models import IsolationPolicy, LifecycleSpec, NetworkPolicySpec, ResourcesSpec, Sandbox, SandboxPhase, SandboxSpec, SchedulingSpec, TemplateSpec

_GROUP = "sandbox.e2b.io"
_VERSION = "v1alpha1"
_PLURAL = "sandboxes"


class SandboxClient:
    """Synchronous client for managing Sandbox resources via the Kubernetes REST API.

    Parameters
    ----------
    auth:
        Authentication configuration produced by one of the helpers in
        :mod:`vm_operator_sdk.auth` (e.g. :func:`~vm_operator_sdk.auth.from_kubeconfig`).
    default_namespace:
        Namespace used when no explicit namespace is supplied to a method.
    """

    def __init__(
        self,
        auth: AuthConfig,
        *,
        default_namespace: str = "default",
    ) -> None:
        self._auth = auth
        self._default_namespace = default_namespace

    # ------------------------------------------------------------------
    # Internal helpers
    # ------------------------------------------------------------------

    def _url(self, namespace: str, name: str = "") -> str:
        base = (
            f"{self._auth.server}/apis/{_GROUP}/{_VERSION}"
            f"/namespaces/{namespace}/{_PLURAL}"
        )
        return f"{base}/{name}" if name else base

    def _request(
        self,
        method: str,
        url: str,
        body: Optional[dict] = None,
        content_type: str = "application/json",
    ) -> dict:
        """Execute an HTTP request and return the parsed JSON response."""
        data: Optional[bytes] = None
        if body is not None:
            data = json.dumps(body).encode()

        headers = self._auth.request_headers()
        headers["Content-Type"] = content_type

        ssl_ctx = self._auth.ssl_context()
        req = urllib.request.Request(url, data=data, headers=headers, method=method)
        try:
            with urllib.request.urlopen(req, context=ssl_ctx) as resp:
                return json.loads(resp.read())
        except urllib.error.HTTPError as exc:
            body_text = exc.read().decode(errors="replace")
            try:
                err_obj = json.loads(body_text)
                msg = err_obj.get("message", body_text)
            except (json.JSONDecodeError, AttributeError):
                msg = body_text
            if exc.code == 404:
                raise SandboxNotFoundError("", "") from exc
            if exc.code == 409:
                raise SandboxAlreadyExistsError("", "") from exc
            raise APIError(exc.code, msg) from exc

    # ------------------------------------------------------------------
    # CRUD operations
    # ------------------------------------------------------------------

    def create(
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
        """Create a new Sandbox and return the server response.

        Parameters
        ----------
        name:
            Name of the Sandbox resource.
        namespace:
            Target namespace.  Defaults to ``default_namespace``.
        template_id:
            Template identifier for the MicroVM image.
        vcpu:
            Number of virtual CPUs (1–64).
        memory_mb:
            Memory in MiB (128–65536).
        disk_mb:
            Disk size in MiB.
        timeout_seconds:
            Maximum lifetime of the sandbox before it is automatically killed.
        node_selector:
            Optional node-selector key/value pairs for scheduling.
        node_name:
            Pin the launcher Pod to a specific node.
        isolation_policy:
            Network isolation policy (``IsolationPolicy.NONE`` or
            ``IsolationPolicy.DEFAULT``).
        sandbox_metadata:
            Arbitrary key/value metadata attached to the Sandbox.
        labels:
            Kubernetes labels added to the Sandbox object's metadata.
        annotations:
            Kubernetes annotations added to the Sandbox object's metadata.

        Returns
        -------
        Sandbox
            The Sandbox object as returned by the API server.

        Raises
        ------
        SandboxAlreadyExistsError
            If a Sandbox with the same name already exists in the namespace.
        APIError
            On unexpected HTTP errors.
        """
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
        raw = self._request("POST", self._url(ns), body=manifest)
        result = Sandbox.from_manifest(raw)
        # Patch name/namespace from the parsed response; fix up AlreadyExists exc.
        return result

    def get(self, name: str, *, namespace: Optional[str] = None) -> Sandbox:
        """Retrieve a Sandbox by name.

        Raises
        ------
        SandboxNotFoundError
            If the Sandbox does not exist.
        """
        ns = namespace or self._default_namespace
        try:
            raw = self._request("GET", self._url(ns, name))
        except SandboxNotFoundError:
            raise SandboxNotFoundError(name, ns)
        return Sandbox.from_manifest(raw)

    def list(self, *, namespace: Optional[str] = None) -> List[Sandbox]:
        """Return all Sandboxes in *namespace*."""
        ns = namespace or self._default_namespace
        raw = self._request("GET", self._url(ns))
        return [Sandbox.from_manifest(item) for item in raw.get("items", [])]

    def delete(self, name: str, *, namespace: Optional[str] = None) -> None:
        """Delete a Sandbox.

        Raises
        ------
        SandboxNotFoundError
            If the Sandbox does not exist.
        """
        ns = namespace or self._default_namespace
        try:
            self._request("DELETE", self._url(ns, name))
        except SandboxNotFoundError:
            raise SandboxNotFoundError(name, ns)

    def pause(self, name: str, *, namespace: Optional[str] = None) -> Sandbox:
        """Pause a running Sandbox (snapshot the MicroVM and release the node).

        Sets ``spec.paused = true`` via a JSON merge patch.

        Raises
        ------
        SandboxNotFoundError
            If the Sandbox does not exist.
        """
        ns = namespace or self._default_namespace
        patch = {"spec": {"paused": True}}
        try:
            raw = self._request(
                "PATCH",
                self._url(ns, name),
                body=patch,
                content_type="application/merge-patch+json",
            )
        except SandboxNotFoundError:
            raise SandboxNotFoundError(name, ns)
        return Sandbox.from_manifest(raw)

    def resume(self, name: str, *, namespace: Optional[str] = None) -> Sandbox:
        """Resume a paused Sandbox (restore from snapshot).

        Sets ``spec.paused = false`` via a JSON merge patch.

        Raises
        ------
        SandboxNotFoundError
            If the Sandbox does not exist.
        """
        ns = namespace or self._default_namespace
        patch = {"spec": {"paused": False}}
        try:
            raw = self._request(
                "PATCH",
                self._url(ns, name),
                body=patch,
                content_type="application/merge-patch+json",
            )
        except SandboxNotFoundError:
            raise SandboxNotFoundError(name, ns)
        return Sandbox.from_manifest(raw)

    def patch_labels(
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
            raw = self._request(
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

    def wait_until_running(
        self,
        name: str,
        *,
        namespace: Optional[str] = None,
        timeout: float = 120.0,
        poll_interval: float = 2.0,
    ) -> Sandbox:
        """Poll until the Sandbox reaches the ``Running`` phase.

        Parameters
        ----------
        name:
            Name of the Sandbox to wait for.
        namespace:
            Namespace of the Sandbox.
        timeout:
            Maximum number of seconds to wait.
        poll_interval:
            Seconds between each poll.

        Returns
        -------
        Sandbox
            The Sandbox in the ``Running`` phase.

        Raises
        ------
        TimeoutError
            If the Sandbox does not reach ``Running`` within *timeout* seconds.
        APIError
            If the Sandbox transitions to a terminal failure phase.
        """
        ns = namespace or self._default_namespace
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            sandbox = self.get(name, namespace=ns)
            if sandbox.phase == SandboxPhase.RUNNING:
                return sandbox
            if sandbox.status and sandbox.status.is_terminal:
                raise APIError(
                    0,
                    f"Sandbox '{name}' entered terminal phase: {sandbox.phase}",
                )
            time.sleep(poll_interval)
        raise TimeoutError(
            f"Sandbox '{name}' did not reach Running within {timeout}s"
        )

    def create_and_wait(
        self,
        name: str,
        *,
        namespace: Optional[str] = None,
        timeout: float = 120.0,
        **create_kwargs,
    ) -> Sandbox:
        """Create a Sandbox and block until it is Running.

        Combines :meth:`create` and :meth:`wait_until_running` for one-line
        usage in AI agent / code-interpreter notebooks.

        Parameters
        ----------
        name:
            Name of the Sandbox resource.
        namespace:
            Target namespace.
        timeout:
            Seconds to wait for the Sandbox to become Running.
        **create_kwargs:
            Additional keyword arguments forwarded to :meth:`create`.

        Returns
        -------
        Sandbox
            The Sandbox in the ``Running`` phase.
        """
        ns = namespace or self._default_namespace
        self.create(name, namespace=ns, **create_kwargs)
        return self.wait_until_running(name, namespace=ns, timeout=timeout)

    def create_batch(
        self,
        names: List[str],
        *,
        namespace: Optional[str] = None,
        **create_kwargs,
    ) -> List[Sandbox]:
        """Create multiple Sandboxes and return immediately (no wait).

        Parameters
        ----------
        names:
            List of Sandbox names to create.
        namespace:
            Target namespace for all Sandboxes.
        **create_kwargs:
            Additional keyword arguments forwarded to :meth:`create`.

        Returns
        -------
        list[Sandbox]
            The created Sandbox objects (phase will be ``Pending`` or
            ``Scheduling``).
        """
        ns = namespace or self._default_namespace
        results: List[Sandbox] = []
        for n in names:
            results.append(self.create(n, namespace=ns, **create_kwargs))
        return results

    def delete_batch(
        self,
        names: List[str],
        *,
        namespace: Optional[str] = None,
        ignore_not_found: bool = True,
    ) -> None:
        """Delete multiple Sandboxes.

        Parameters
        ----------
        names:
            Sandbox names to delete.
        namespace:
            Target namespace for all Sandboxes.
        ignore_not_found:
            When ``True`` (default), :exc:`SandboxNotFoundError` is silenced.
        """
        ns = namespace or self._default_namespace
        for n in names:
            try:
                self.delete(n, namespace=ns)
            except SandboxNotFoundError:
                if not ignore_not_found:
                    raise

    def iter_sandboxes(
        self, *, namespace: Optional[str] = None
    ) -> Iterator[Sandbox]:
        """Iterate over all Sandboxes in *namespace*."""
        yield from self.list(namespace=namespace)

    # ------------------------------------------------------------------
    # Context manager support
    # ------------------------------------------------------------------

    def __enter__(self) -> "SandboxClient":
        return self

    def __exit__(self, *_) -> None:
        self._auth.cleanup()
