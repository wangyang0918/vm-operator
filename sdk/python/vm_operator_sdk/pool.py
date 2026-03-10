"""VM pool manager for pre-warmed Sandbox VMs.

A :class:`SandboxPool` (synchronous) or :class:`AsyncSandboxPool` (async)
maintains a set of pre-created, idle Sandbox resources.  Callers
:meth:`~SandboxPool.acquire` a ready-to-use VM from the pool and
:meth:`~SandboxPool.release` it when done, avoiding the cold-start latency of
booting a new MicroVM for every request.

Pool membership and state are tracked with two Kubernetes labels:

* ``vm-operator/pool``       – the pool name (used to find all members)
* ``vm-operator/pool-state`` – current state: ``idle`` or ``in-use``

Example (sync)::

    import vm_operator_sdk as sdk

    auth = sdk.from_kubeconfig()
    client = sdk.SandboxClient(auth)

    pool = sdk.SandboxPool(
        client,
        pool_name="my-pool",
        size=5,
        template_id="ubuntu-22.04",
        vcpu=2,
        memory_mb=512,
    )
    pool.replenish()  # pre-warm: create idle VMs and wait until Running

    # Acquire a running VM from the pool (no cold-start wait)
    with pool.acquire_context() as sandbox:
        print(sandbox.name, sandbox.phase)
        # ... use the sandbox ...
    # sandbox is automatically released back to the pool

    pool.drain()  # delete all pool VMs when no longer needed

Example (async)::

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
            await pool.replenish()

            async with pool.acquire_context() as sandbox:
                print(sandbox.name)

            await pool.drain()

    asyncio.run(main())
"""

from __future__ import annotations

import asyncio
import time
import uuid
from contextlib import asynccontextmanager, contextmanager
from typing import TYPE_CHECKING, AsyncGenerator, Generator, List, Optional

from .exceptions import PoolExhaustedError, SandboxNotFoundError
from .models import Sandbox, SandboxPhase

if TYPE_CHECKING:
    from .async_client import AsyncSandboxClient
    from .client import SandboxClient

# Well-known label keys used to track pool membership and state.
POOL_LABEL = "vm-operator/pool"
POOL_STATE_LABEL = "vm-operator/pool-state"

# Label values for the two pool states.
STATE_IDLE = "idle"
STATE_IN_USE = "in-use"


def _make_pool_name(pool_name: str) -> str:
    """Return a unique Sandbox name scoped to *pool_name*."""
    return f"{pool_name}-{uuid.uuid4().hex[:8]}"


class SandboxPool:
    """A pool of pre-created idle Sandbox VMs (synchronous).

    Instead of creating a new Sandbox for every request – and waiting for the
    MicroVM to boot – a pool maintains a set of Sandboxes that are already
    ``Running``.  Callers :meth:`acquire` a Sandbox and :meth:`release` it
    when done.

    Pool membership and state are tracked via Kubernetes labels so that the
    pool survives process restarts.

    Parameters
    ----------
    client:
        A :class:`~vm_operator_sdk.client.SandboxClient` used to manage
        Sandbox resources.
    pool_name:
        Unique identifier for this pool.  Used as the value of the
        ``vm-operator/pool`` label on every pool member.
    namespace:
        Kubernetes namespace for pool Sandboxes.
    size:
        Target number of idle Sandboxes to maintain.  :meth:`replenish`
        creates Sandboxes until the idle count reaches this value.
    template_id:
        Template ID for newly created pool Sandboxes.
    vcpu:
        vCPU count for pool Sandboxes.
    memory_mb:
        Memory in MiB for pool Sandboxes.
    disk_mb:
        Disk size in MiB for pool Sandboxes.
    timeout_seconds:
        Maximum lifetime of each pool Sandbox.  Defaults to 3600 (1 hour).
    acquire_timeout:
        Seconds to wait for an idle Sandbox when :meth:`acquire` is called
        and the pool is fully checked out.
    poll_interval:
        Seconds between polls while waiting in :meth:`acquire`.
    """

    def __init__(
        self,
        client: "SandboxClient",
        *,
        pool_name: str,
        namespace: str = "default",
        size: int = 5,
        template_id: str = "",
        vcpu: int = 2,
        memory_mb: int = 512,
        disk_mb: int = 2048,
        timeout_seconds: int = 3600,
        acquire_timeout: float = 60.0,
        poll_interval: float = 2.0,
    ) -> None:
        self._client = client
        self._pool_name = pool_name
        self._namespace = namespace
        self._size = size
        self._template_id = template_id
        self._vcpu = vcpu
        self._memory_mb = memory_mb
        self._disk_mb = disk_mb
        self._timeout_seconds = timeout_seconds
        self._acquire_timeout = acquire_timeout
        self._poll_interval = poll_interval

    # ------------------------------------------------------------------
    # Pool state inspection
    # ------------------------------------------------------------------

    def all_sandboxes(self) -> List[Sandbox]:
        """Return all Sandboxes belonging to this pool (idle and in-use)."""
        return [
            sb
            for sb in self._client.list(namespace=self._namespace)
            if sb.labels.get(POOL_LABEL) == self._pool_name
        ]

    def idle_sandboxes(self) -> List[Sandbox]:
        """Return idle, Running Sandboxes available for acquisition."""
        return [
            sb
            for sb in self.all_sandboxes()
            if sb.labels.get(POOL_STATE_LABEL) == STATE_IDLE
            and sb.phase == SandboxPhase.RUNNING
        ]

    def in_use_sandboxes(self) -> List[Sandbox]:
        """Return Sandboxes currently checked out from the pool."""
        return [
            sb
            for sb in self.all_sandboxes()
            if sb.labels.get(POOL_STATE_LABEL) == STATE_IN_USE
        ]

    # ------------------------------------------------------------------
    # Pool lifecycle
    # ------------------------------------------------------------------

    def replenish(
        self,
        *,
        wait: bool = True,
        wait_timeout: float = 120.0,
    ) -> List[Sandbox]:
        """Create Sandboxes to bring the idle count up to *size*.

        Parameters
        ----------
        wait:
            When ``True`` (default), block until every newly created Sandbox
            reaches the ``Running`` phase before returning.
        wait_timeout:
            Per-Sandbox seconds to wait for ``Running`` when *wait* is
            ``True``.

        Returns
        -------
        list[Sandbox]
            Newly created Sandbox objects (already ``Running`` when
            *wait* is ``True``).
        """
        idle = self.idle_sandboxes()
        needed = max(0, self._size - len(idle))
        created: List[Sandbox] = []
        for _ in range(needed):
            name = _make_pool_name(self._pool_name)
            labels = {POOL_LABEL: self._pool_name, POOL_STATE_LABEL: STATE_IDLE}
            if wait:
                sb = self._client.create_and_wait(
                    name,
                    namespace=self._namespace,
                    template_id=self._template_id,
                    vcpu=self._vcpu,
                    memory_mb=self._memory_mb,
                    disk_mb=self._disk_mb,
                    timeout_seconds=self._timeout_seconds,
                    labels=labels,
                    timeout=wait_timeout,
                )
            else:
                sb = self._client.create(
                    name,
                    namespace=self._namespace,
                    template_id=self._template_id,
                    vcpu=self._vcpu,
                    memory_mb=self._memory_mb,
                    disk_mb=self._disk_mb,
                    timeout_seconds=self._timeout_seconds,
                    labels=labels,
                )
            created.append(sb)
        return created

    def drain(self, *, ignore_errors: bool = True) -> None:
        """Delete all Sandboxes in the pool (idle and in-use).

        Parameters
        ----------
        ignore_errors:
            When ``True`` (default), errors during deletion are silenced so
            that as many Sandboxes as possible are removed.
        """
        for sb in self.all_sandboxes():
            try:
                self._client.delete(sb.name, namespace=self._namespace)
            except SandboxNotFoundError:
                pass
            except Exception:
                if not ignore_errors:
                    raise

    # ------------------------------------------------------------------
    # Acquire / release
    # ------------------------------------------------------------------

    def acquire(self) -> Sandbox:
        """Acquire an idle Sandbox from the pool.

        Polls the pool until a ``Running`` Sandbox with state ``idle`` is
        found, marks it ``in-use``, and returns it.

        If multiple callers race to acquire the same idle Sandbox, the loser
        will transparently retry on the next idle Sandbox in the list.

        Returns
        -------
        Sandbox
            The acquired Sandbox (``vm-operator/pool-state`` label is now
            ``in-use``).

        Raises
        ------
        PoolExhaustedError
            If no idle Sandbox becomes available within *acquire_timeout*
            seconds.
        """
        deadline = time.monotonic() + self._acquire_timeout
        while time.monotonic() < deadline:
            idle = self.idle_sandboxes()
            for sb in idle:
                self._client.patch_labels(
                    sb.name,
                    {POOL_STATE_LABEL: STATE_IN_USE},
                    namespace=self._namespace,
                )
                # Re-read to get the latest state after patching.
                refreshed = self._client.get(sb.name, namespace=self._namespace)
                if refreshed.labels.get(POOL_STATE_LABEL) == STATE_IN_USE:
                    return refreshed
            time.sleep(self._poll_interval)
        raise PoolExhaustedError(
            self._pool_name,
            f"No idle Sandbox available in pool '{self._pool_name}' "
            f"after {self._acquire_timeout}s",
        )

    def release(self, name: str) -> None:
        """Return a Sandbox to the pool by marking it ``idle``.

        Parameters
        ----------
        name:
            Name of the Sandbox to return.
        """
        self._client.patch_labels(
            name,
            {POOL_STATE_LABEL: STATE_IDLE},
            namespace=self._namespace,
        )

    @contextmanager
    def acquire_context(self) -> Generator[Sandbox, None, None]:
        """Context manager that acquires and automatically releases a Sandbox.

        Usage::

            with pool.acquire_context() as sandbox:
                print(sandbox.name)
                # ... use the sandbox ...
            # sandbox is released back to the pool here

        Raises
        ------
        PoolExhaustedError
            If no idle Sandbox is available within *acquire_timeout*.
        """
        sb = self.acquire()
        try:
            yield sb
        finally:
            self.release(sb.name)


class AsyncSandboxPool:
    """A pool of pre-created idle Sandbox VMs (asynchronous).

    Provides the same semantics as :class:`SandboxPool` but every method is
    a coroutine, suitable for use with ``asyncio``.

    Parameters
    ----------
    client:
        An :class:`~vm_operator_sdk.async_client.AsyncSandboxClient`.
    pool_name:
        Unique identifier for this pool.
    namespace:
        Kubernetes namespace for pool Sandboxes.
    size:
        Target number of idle Sandboxes.
    template_id:
        Template ID for newly created pool Sandboxes.
    vcpu:
        vCPU count for pool Sandboxes.
    memory_mb:
        Memory in MiB for pool Sandboxes.
    disk_mb:
        Disk size in MiB for pool Sandboxes.
    timeout_seconds:
        Maximum lifetime of each pool Sandbox.  Defaults to 3600 (1 hour).
    acquire_timeout:
        Seconds to wait for an idle Sandbox.
    poll_interval:
        Seconds between polls while waiting in :meth:`acquire`.
    """

    def __init__(
        self,
        client: "AsyncSandboxClient",
        *,
        pool_name: str,
        namespace: str = "default",
        size: int = 5,
        template_id: str = "",
        vcpu: int = 2,
        memory_mb: int = 512,
        disk_mb: int = 2048,
        timeout_seconds: int = 3600,
        acquire_timeout: float = 60.0,
        poll_interval: float = 2.0,
    ) -> None:
        self._client = client
        self._pool_name = pool_name
        self._namespace = namespace
        self._size = size
        self._template_id = template_id
        self._vcpu = vcpu
        self._memory_mb = memory_mb
        self._disk_mb = disk_mb
        self._timeout_seconds = timeout_seconds
        self._acquire_timeout = acquire_timeout
        self._poll_interval = poll_interval

    # ------------------------------------------------------------------
    # Pool state inspection
    # ------------------------------------------------------------------

    async def all_sandboxes(self) -> List[Sandbox]:
        """Return all Sandboxes belonging to this pool (idle and in-use)."""
        return [
            sb
            for sb in await self._client.list(namespace=self._namespace)
            if sb.labels.get(POOL_LABEL) == self._pool_name
        ]

    async def idle_sandboxes(self) -> List[Sandbox]:
        """Return idle, Running Sandboxes available for acquisition."""
        return [
            sb
            for sb in await self.all_sandboxes()
            if sb.labels.get(POOL_STATE_LABEL) == STATE_IDLE
            and sb.phase == SandboxPhase.RUNNING
        ]

    async def in_use_sandboxes(self) -> List[Sandbox]:
        """Return Sandboxes currently checked out from the pool."""
        return [
            sb
            for sb in await self.all_sandboxes()
            if sb.labels.get(POOL_STATE_LABEL) == STATE_IN_USE
        ]

    # ------------------------------------------------------------------
    # Pool lifecycle
    # ------------------------------------------------------------------

    async def replenish(
        self,
        *,
        wait: bool = True,
        wait_timeout: float = 120.0,
    ) -> List[Sandbox]:
        """Create Sandboxes to bring the idle count up to *size*.

        Parameters
        ----------
        wait:
            When ``True`` (default), await until every newly created Sandbox
            reaches the ``Running`` phase.
        wait_timeout:
            Per-Sandbox seconds to wait for ``Running``.

        Returns
        -------
        list[Sandbox]
            Newly created Sandbox objects.
        """
        idle = await self.idle_sandboxes()
        needed = max(0, self._size - len(idle))
        labels = {POOL_LABEL: self._pool_name, POOL_STATE_LABEL: STATE_IDLE}
        create_kwargs = dict(
            namespace=self._namespace,
            template_id=self._template_id,
            vcpu=self._vcpu,
            memory_mb=self._memory_mb,
            disk_mb=self._disk_mb,
            timeout_seconds=self._timeout_seconds,
            labels=labels,
        )
        if wait:
            tasks = [
                asyncio.create_task(
                    self._client.create_and_wait(
                        _make_pool_name(self._pool_name),
                        timeout=wait_timeout,
                        **create_kwargs,
                    )
                )
                for _ in range(needed)
            ]
        else:
            tasks = [
                asyncio.create_task(
                    self._client.create(
                        _make_pool_name(self._pool_name),
                        **create_kwargs,
                    )
                )
                for _ in range(needed)
            ]
        return list(await asyncio.gather(*tasks))

    async def drain(self, *, ignore_errors: bool = True) -> None:
        """Delete all Sandboxes in the pool (idle and in-use).

        Parameters
        ----------
        ignore_errors:
            When ``True`` (default), errors during deletion are silenced.
        """
        async def _delete_one(sb: Sandbox) -> None:
            try:
                await self._client.delete(sb.name, namespace=self._namespace)
            except SandboxNotFoundError:
                pass
            except Exception:
                if not ignore_errors:
                    raise

        await asyncio.gather(*[_delete_one(sb) for sb in await self.all_sandboxes()])

    # ------------------------------------------------------------------
    # Acquire / release
    # ------------------------------------------------------------------

    async def acquire(self) -> Sandbox:
        """Acquire an idle Sandbox from the pool.

        Polls the pool until a ``Running`` Sandbox with state ``idle`` is
        found, marks it ``in-use``, and returns it.

        If multiple callers race to acquire the same idle Sandbox, the loser
        will transparently retry on the next idle Sandbox in the list.

        Returns
        -------
        Sandbox
            The acquired Sandbox.

        Raises
        ------
        PoolExhaustedError
            If no idle Sandbox becomes available within *acquire_timeout*.
        """
        loop = asyncio.get_running_loop()
        deadline = loop.time() + self._acquire_timeout
        while loop.time() < deadline:
            idle = await self.idle_sandboxes()
            for sb in idle:
                await self._client.patch_labels(
                    sb.name,
                    {POOL_STATE_LABEL: STATE_IN_USE},
                    namespace=self._namespace,
                )
                # Re-read to get the latest state after patching.
                refreshed = await self._client.get(sb.name, namespace=self._namespace)
                if refreshed.labels.get(POOL_STATE_LABEL) == STATE_IN_USE:
                    return refreshed
            await asyncio.sleep(self._poll_interval)
        raise PoolExhaustedError(
            self._pool_name,
            f"No idle Sandbox available in pool '{self._pool_name}' "
            f"after {self._acquire_timeout}s",
        )

    async def release(self, name: str) -> None:
        """Return a Sandbox to the pool by marking it ``idle``.

        Parameters
        ----------
        name:
            Name of the Sandbox to return.
        """
        await self._client.patch_labels(
            name,
            {POOL_STATE_LABEL: STATE_IDLE},
            namespace=self._namespace,
        )

    @asynccontextmanager
    async def acquire_context(self) -> AsyncGenerator[Sandbox, None]:
        """Async context manager that acquires and releases a Sandbox.

        Usage::

            async with pool.acquire_context() as sandbox:
                print(sandbox.name)
                # ... use the sandbox ...
            # sandbox is released back to the pool here

        Raises
        ------
        PoolExhaustedError
            If no idle Sandbox is available within *acquire_timeout*.
        """
        sb = await self.acquire()
        try:
            yield sb
        finally:
            await self.release(sb.name)
