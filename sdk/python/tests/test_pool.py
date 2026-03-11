"""Unit tests for vm_operator_sdk.pool (SandboxPool and AsyncSandboxPool).

All Kubernetes calls are intercepted with ``unittest.mock`` so no real cluster
is needed.
"""

from __future__ import annotations

import time
from typing import Any, Dict, List
from unittest.mock import AsyncMock, MagicMock, patch

import pytest

from vm_operator_sdk.exceptions import PoolExhaustedError
from vm_operator_sdk.models import Sandbox, SandboxPhase, SandboxStatus
from vm_operator_sdk.pool import (
    POOL_LABEL,
    POOL_STATE_LABEL,
    STATE_IDLE,
    STATE_IN_USE,
    AsyncSandboxPool,
    SandboxPool,
)

from .conftest import make_sandbox_manifest


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _make_pool_sandbox(
    name: str,
    pool_name: str,
    state: str = STATE_IDLE,
    phase: SandboxPhase = SandboxPhase.RUNNING,
) -> Sandbox:
    """Return a Sandbox that belongs to *pool_name* with the given *state*."""
    sb = Sandbox(
        name=name,
        namespace="default",
        labels={POOL_LABEL: pool_name, POOL_STATE_LABEL: state},
    )
    sb.status = SandboxStatus(phase=phase)
    return sb


def _mock_client(
    list_return: List[Sandbox] | None = None,
    get_return: Sandbox | None = None,
) -> MagicMock:
    """Return a mock SandboxClient with commonly used methods pre-wired."""
    client = MagicMock()
    client.list.return_value = list_return or []
    if get_return is not None:
        client.get.return_value = get_return
    client.create.return_value = _make_pool_sandbox("new-sb", "test-pool")
    client.create_and_wait.return_value = _make_pool_sandbox("new-sb", "test-pool")
    client.patch_labels.return_value = _make_pool_sandbox(
        "new-sb", "test-pool", state=STATE_IN_USE
    )
    client.delete.return_value = None
    return client


def _pool(client: MagicMock, **kwargs) -> SandboxPool:
    defaults = dict(
        pool_name="test-pool",
        namespace="default",
        size=2,
        template_id="ubuntu-22.04",
        acquire_timeout=0.1,
        poll_interval=0.01,
    )
    defaults.update(kwargs)
    return SandboxPool(client, **defaults)


# ---------------------------------------------------------------------------
# SandboxPool – state inspection
# ---------------------------------------------------------------------------


class TestSandboxPoolInspection:
    def test_all_sandboxes_filters_by_pool_name(self):
        sb_in_pool = _make_pool_sandbox("sb1", "test-pool")
        sb_other = _make_pool_sandbox("sb2", "other-pool")
        client = _mock_client(list_return=[sb_in_pool, sb_other])
        pool = _pool(client)
        assert pool.all_sandboxes() == [sb_in_pool]

    def test_idle_sandboxes_requires_running(self):
        idle_running = _make_pool_sandbox("sb1", "test-pool", STATE_IDLE, SandboxPhase.RUNNING)
        idle_pending = _make_pool_sandbox("sb2", "test-pool", STATE_IDLE, SandboxPhase.PENDING)
        in_use = _make_pool_sandbox("sb3", "test-pool", STATE_IN_USE, SandboxPhase.RUNNING)
        client = _mock_client(list_return=[idle_running, idle_pending, in_use])
        pool = _pool(client)
        result = pool.idle_sandboxes()
        assert result == [idle_running]

    def test_in_use_sandboxes(self):
        idle = _make_pool_sandbox("sb1", "test-pool", STATE_IDLE)
        in_use = _make_pool_sandbox("sb2", "test-pool", STATE_IN_USE)
        client = _mock_client(list_return=[idle, in_use])
        pool = _pool(client)
        assert pool.in_use_sandboxes() == [in_use]


# ---------------------------------------------------------------------------
# SandboxPool – replenish
# ---------------------------------------------------------------------------


class TestSandboxPoolReplenish:
    def test_replenish_creates_needed_sandboxes(self):
        # Pool size=2, zero idle → should create 2 sandboxes.
        client = _mock_client(list_return=[])
        pool = _pool(client, size=2)
        created = pool.replenish(wait=False)
        assert len(created) == 2
        assert client.create.call_count == 2

    def test_replenish_respects_existing_idle(self):
        # Pool size=3, one already idle → should create 2.
        idle_sb = _make_pool_sandbox("existing", "test-pool", STATE_IDLE)
        client = _mock_client(list_return=[idle_sb])
        pool = _pool(client, size=3)
        pool.replenish(wait=False)
        assert client.create.call_count == 2

    def test_replenish_wait_uses_create_and_wait(self):
        client = _mock_client(list_return=[])
        pool = _pool(client, size=1)
        pool.replenish(wait=True)
        assert client.create_and_wait.call_count == 1
        client.create.assert_not_called()

    def test_replenish_adds_pool_labels(self):
        client = _mock_client(list_return=[])
        pool = _pool(client, size=1)
        pool.replenish(wait=False)
        _, kwargs = client.create.call_args
        labels = kwargs["labels"]
        assert labels[POOL_LABEL] == "test-pool"
        assert labels[POOL_STATE_LABEL] == STATE_IDLE

    def test_replenish_no_op_when_pool_full(self):
        idle1 = _make_pool_sandbox("sb1", "test-pool", STATE_IDLE)
        idle2 = _make_pool_sandbox("sb2", "test-pool", STATE_IDLE)
        client = _mock_client(list_return=[idle1, idle2])
        pool = _pool(client, size=2)
        created = pool.replenish(wait=False)
        assert created == []
        client.create.assert_not_called()


# ---------------------------------------------------------------------------
# SandboxPool – drain
# ---------------------------------------------------------------------------


class TestSandboxPoolDrain:
    def test_drain_deletes_all_pool_sandboxes(self):
        sb1 = _make_pool_sandbox("sb1", "test-pool", STATE_IDLE)
        sb2 = _make_pool_sandbox("sb2", "test-pool", STATE_IN_USE)
        client = _mock_client(list_return=[sb1, sb2])
        pool = _pool(client)
        pool.drain()
        assert client.delete.call_count == 2

    def test_drain_ignores_not_found_by_default(self):
        from vm_operator_sdk.exceptions import SandboxNotFoundError

        sb = _make_pool_sandbox("sb1", "test-pool", STATE_IDLE)
        client = _mock_client(list_return=[sb])
        client.delete.side_effect = SandboxNotFoundError("sb1", "default")
        pool = _pool(client)
        pool.drain()  # Should not raise


# ---------------------------------------------------------------------------
# SandboxPool – acquire / release
# ---------------------------------------------------------------------------


class TestSandboxPoolAcquire:
    def test_acquire_returns_idle_sandbox(self):
        idle_sb = _make_pool_sandbox("sb1", "test-pool", STATE_IDLE)
        in_use_sb = _make_pool_sandbox("sb1", "test-pool", STATE_IN_USE)
        client = _mock_client(list_return=[idle_sb], get_return=in_use_sb)
        client.patch_labels.return_value = in_use_sb
        pool = _pool(client)
        result = pool.acquire()
        # patch_labels was called to mark as in-use
        client.patch_labels.assert_called_once_with(
            "sb1", {POOL_STATE_LABEL: STATE_IN_USE}, namespace="default"
        )
        assert result is in_use_sb

    def test_acquire_exhausted_raises(self):
        # No idle sandboxes → should raise PoolExhaustedError.
        client = _mock_client(list_return=[])
        pool = _pool(client, acquire_timeout=0.05, poll_interval=0.01)
        with patch("time.sleep"):
            with pytest.raises(PoolExhaustedError) as exc_info:
                pool.acquire()
        assert exc_info.value.pool_name == "test-pool"

    def test_acquire_waits_until_idle_available(self):
        # First list() call returns nothing; second returns an idle sandbox.
        idle_sb = _make_pool_sandbox("sb1", "test-pool", STATE_IDLE)
        in_use_sb = _make_pool_sandbox("sb1", "test-pool", STATE_IN_USE)
        call_count = 0

        def list_side_effect(namespace):
            nonlocal call_count
            call_count += 1
            return [idle_sb] if call_count >= 2 else []

        client = _mock_client(get_return=in_use_sb)
        client.list.side_effect = list_side_effect
        client.patch_labels.return_value = in_use_sb
        pool = _pool(client, acquire_timeout=1.0, poll_interval=0.01)
        with patch("time.sleep"):
            result = pool.acquire()
        assert result is in_use_sb

    def test_release_marks_idle(self):
        idle_sb = _make_pool_sandbox("sb1", "test-pool", STATE_IDLE)
        client = _mock_client()
        client.patch_labels.return_value = idle_sb
        pool = _pool(client)
        pool.release("sb1")
        client.patch_labels.assert_called_once_with(
            "sb1", {POOL_STATE_LABEL: STATE_IDLE}, namespace="default"
        )


class TestSandboxPoolAcquireContext:
    def test_acquire_context_releases_on_exit(self):
        idle_sb = _make_pool_sandbox("sb1", "test-pool", STATE_IDLE)
        in_use_sb = _make_pool_sandbox("sb1", "test-pool", STATE_IN_USE)
        client = _mock_client(list_return=[idle_sb], get_return=in_use_sb)
        client.patch_labels.side_effect = [in_use_sb, idle_sb]
        pool = _pool(client)
        with pool.acquire_context() as sb:
            assert sb is in_use_sb
        # Second patch_labels call marks it idle again.
        assert client.patch_labels.call_count == 2
        last_call_labels = client.patch_labels.call_args_list[-1][0][1]
        assert last_call_labels[POOL_STATE_LABEL] == STATE_IDLE

    def test_acquire_context_releases_on_exception(self):
        idle_sb = _make_pool_sandbox("sb1", "test-pool", STATE_IDLE)
        in_use_sb = _make_pool_sandbox("sb1", "test-pool", STATE_IN_USE)
        client = _mock_client(list_return=[idle_sb], get_return=in_use_sb)
        client.patch_labels.return_value = in_use_sb
        pool = _pool(client)
        with pytest.raises(RuntimeError):
            with pool.acquire_context():
                raise RuntimeError("boom")
        # release() was still called
        assert client.patch_labels.call_count == 2


# ---------------------------------------------------------------------------
# AsyncSandboxPool
# ---------------------------------------------------------------------------


def _async_mock_client(
    list_return: List[Sandbox] | None = None,
    get_return: Sandbox | None = None,
) -> MagicMock:
    """Return a mock AsyncSandboxClient."""
    client = MagicMock()
    client.list = AsyncMock(return_value=list_return or [])
    if get_return is not None:
        client.get = AsyncMock(return_value=get_return)
    client.create = AsyncMock(return_value=_make_pool_sandbox("new-sb", "test-pool"))
    client.create_and_wait = AsyncMock(
        return_value=_make_pool_sandbox("new-sb", "test-pool")
    )
    client.patch_labels = AsyncMock(
        return_value=_make_pool_sandbox("new-sb", "test-pool", state=STATE_IN_USE)
    )
    client.delete = AsyncMock(return_value=None)
    return client


def _async_pool(client: MagicMock, **kwargs) -> AsyncSandboxPool:
    defaults = dict(
        pool_name="test-pool",
        namespace="default",
        size=2,
        template_id="ubuntu-22.04",
        acquire_timeout=0.1,
        poll_interval=0.01,
    )
    defaults.update(kwargs)
    return AsyncSandboxPool(client, **defaults)


@pytest.mark.asyncio
class TestAsyncSandboxPoolInspection:
    async def test_all_sandboxes_filters_by_pool(self):
        sb_in = _make_pool_sandbox("sb1", "test-pool")
        sb_out = _make_pool_sandbox("sb2", "other-pool")
        client = _async_mock_client(list_return=[sb_in, sb_out])
        pool = _async_pool(client)
        assert await pool.all_sandboxes() == [sb_in]

    async def test_idle_sandboxes_running_only(self):
        idle = _make_pool_sandbox("sb1", "test-pool", STATE_IDLE, SandboxPhase.RUNNING)
        not_running = _make_pool_sandbox("sb2", "test-pool", STATE_IDLE, SandboxPhase.PENDING)
        client = _async_mock_client(list_return=[idle, not_running])
        pool = _async_pool(client)
        assert await pool.idle_sandboxes() == [idle]


@pytest.mark.asyncio
class TestAsyncSandboxPoolReplenish:
    async def test_replenish_creates_needed(self):
        client = _async_mock_client(list_return=[])
        pool = _async_pool(client, size=3)
        created = await pool.replenish(wait=False)
        assert len(created) == 3
        assert client.create.call_count == 3

    async def test_replenish_wait_uses_create_and_wait(self):
        client = _async_mock_client(list_return=[])
        pool = _async_pool(client, size=2)
        await pool.replenish(wait=True)
        assert client.create_and_wait.call_count == 2

    async def test_replenish_no_op_when_full(self):
        idle1 = _make_pool_sandbox("sb1", "test-pool", STATE_IDLE)
        idle2 = _make_pool_sandbox("sb2", "test-pool", STATE_IDLE)
        client = _async_mock_client(list_return=[idle1, idle2])
        pool = _async_pool(client, size=2)
        created = await pool.replenish(wait=False)
        assert created == []


@pytest.mark.asyncio
class TestAsyncSandboxPoolDrain:
    async def test_drain_deletes_all(self):
        sb1 = _make_pool_sandbox("sb1", "test-pool")
        sb2 = _make_pool_sandbox("sb2", "test-pool")
        client = _async_mock_client(list_return=[sb1, sb2])
        pool = _async_pool(client)
        await pool.drain()
        assert client.delete.call_count == 2


@pytest.mark.asyncio
class TestAsyncSandboxPoolAcquire:
    async def test_acquire_marks_in_use(self):
        idle_sb = _make_pool_sandbox("sb1", "test-pool", STATE_IDLE)
        in_use_sb = _make_pool_sandbox("sb1", "test-pool", STATE_IN_USE)
        client = _async_mock_client(list_return=[idle_sb], get_return=in_use_sb)
        client.patch_labels = AsyncMock(return_value=in_use_sb)
        pool = _async_pool(client)
        result = await pool.acquire()
        client.patch_labels.assert_called_once_with(
            "sb1", {POOL_STATE_LABEL: STATE_IN_USE}, namespace="default"
        )
        assert result is in_use_sb

    async def test_acquire_exhausted_raises(self):
        client = _async_mock_client(list_return=[])
        pool = _async_pool(client, acquire_timeout=0.05, poll_interval=0.01)
        with patch("asyncio.sleep", new=AsyncMock()):
            with pytest.raises(PoolExhaustedError) as exc_info:
                await pool.acquire()
        assert exc_info.value.pool_name == "test-pool"

    async def test_release_marks_idle(self):
        idle_sb = _make_pool_sandbox("sb1", "test-pool", STATE_IDLE)
        client = _async_mock_client()
        client.patch_labels = AsyncMock(return_value=idle_sb)
        pool = _async_pool(client)
        await pool.release("sb1")
        client.patch_labels.assert_called_once_with(
            "sb1", {POOL_STATE_LABEL: STATE_IDLE}, namespace="default"
        )

    async def test_acquire_context_releases_on_exit(self):
        idle_sb = _make_pool_sandbox("sb1", "test-pool", STATE_IDLE)
        in_use_sb = _make_pool_sandbox("sb1", "test-pool", STATE_IN_USE)
        client = _async_mock_client(list_return=[idle_sb], get_return=in_use_sb)
        client.patch_labels = AsyncMock(side_effect=[in_use_sb, idle_sb])
        pool = _async_pool(client)
        async with pool.acquire_context() as sb:
            assert sb is in_use_sb
        assert client.patch_labels.call_count == 2
        last_labels = client.patch_labels.call_args_list[-1][0][1]
        assert last_labels[POOL_STATE_LABEL] == STATE_IDLE

    async def test_acquire_context_releases_on_exception(self):
        idle_sb = _make_pool_sandbox("sb1", "test-pool", STATE_IDLE)
        in_use_sb = _make_pool_sandbox("sb1", "test-pool", STATE_IN_USE)
        client = _async_mock_client(list_return=[idle_sb], get_return=in_use_sb)
        client.patch_labels = AsyncMock(return_value=in_use_sb)
        pool = _async_pool(client)
        with pytest.raises(RuntimeError):
            async with pool.acquire_context():
                raise RuntimeError("boom")
        assert client.patch_labels.call_count == 2
