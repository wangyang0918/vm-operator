"""Unit tests for vm_operator_sdk.async_client (AsyncSandboxClient).

All outbound HTTP calls are intercepted with ``unittest.mock`` / ``MagicMock``
so no real Kubernetes cluster or network access is required.
"""

from __future__ import annotations

import asyncio
import json
from typing import Any, Dict
from unittest.mock import AsyncMock, MagicMock, patch

import pytest

from vm_operator_sdk.async_client import AsyncSandboxClient
from vm_operator_sdk.exceptions import (
    APIError,
    SandboxAlreadyExistsError,
    SandboxNotFoundError,
    TimeoutError,
)
from vm_operator_sdk.models import SandboxPhase

from .conftest import make_list_manifest, make_sandbox_manifest


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _make_mock_response(body: Dict[str, Any], status: int = 200) -> MagicMock:
    """Return a mock aiohttp response context manager."""
    resp = MagicMock()
    resp.status = status
    resp.text = AsyncMock(return_value=json.dumps(body))
    # Support ``async with session.request(...) as resp:``
    cm = MagicMock()
    cm.__aenter__ = AsyncMock(return_value=resp)
    cm.__aexit__ = AsyncMock(return_value=False)
    return cm


def _make_session_mock(response_cm) -> MagicMock:
    """Return a mock aiohttp ClientSession whose request() returns response_cm."""
    session = MagicMock()
    session.closed = False
    session.request = MagicMock(return_value=response_cm)
    session.close = AsyncMock()
    return session


def _patched_client(auth, response_body, status=200, default_namespace="default"):
    """Create an AsyncSandboxClient with a pre-wired mock session."""
    client = AsyncSandboxClient(auth, default_namespace=default_namespace)
    resp_cm = _make_mock_response(response_body, status)
    client._session = _make_session_mock(resp_cm)
    return client


# ---------------------------------------------------------------------------
# Tests
# ---------------------------------------------------------------------------


@pytest.mark.asyncio
class TestAsyncSandboxClientCreate:
    async def test_create_returns_sandbox(self, auth):
        manifest = make_sandbox_manifest("sb1")
        client = _patched_client(auth, manifest)
        sb = await client.create("sb1", template_id="ubuntu-22.04")
        assert sb.name == "sb1"

    async def test_create_already_exists_raises(self, auth):
        client = _patched_client(
            auth, {"message": "already exists"}, status=409
        )
        with pytest.raises(SandboxAlreadyExistsError):
            await client.create("sb1")

    async def test_create_api_error_raises(self, auth):
        client = _patched_client(auth, {"message": "internal"}, status=500)
        with pytest.raises(APIError) as exc_info:
            await client.create("sb1")
        assert exc_info.value.status_code == 500


@pytest.mark.asyncio
class TestAsyncSandboxClientGet:
    async def test_get_returns_sandbox(self, auth):
        manifest = make_sandbox_manifest("sb1", phase="Running")
        client = _patched_client(auth, manifest)
        sb = await client.get("sb1")
        assert sb.phase == SandboxPhase.RUNNING

    async def test_get_not_found_raises(self, auth):
        client = _patched_client(auth, {}, status=404)
        with pytest.raises(SandboxNotFoundError) as exc_info:
            await client.get("missing")
        assert exc_info.value.name == "missing"


@pytest.mark.asyncio
class TestAsyncSandboxClientList:
    async def test_list_empty(self, auth):
        client = _patched_client(auth, make_list_manifest([]))
        assert await client.list() == []

    async def test_list_multiple(self, auth):
        raw = make_list_manifest(
            [make_sandbox_manifest("sb1"), make_sandbox_manifest("sb2")]
        )
        client = _patched_client(auth, raw)
        result = await client.list()
        assert len(result) == 2


@pytest.mark.asyncio
class TestAsyncSandboxClientDelete:
    async def test_delete_ok(self, auth):
        client = _patched_client(auth, {})
        await client.delete("sb1")  # Should not raise

    async def test_delete_not_found_raises(self, auth):
        client = _patched_client(auth, {}, status=404)
        with pytest.raises(SandboxNotFoundError):
            await client.delete("missing")


@pytest.mark.asyncio
class TestAsyncSandboxClientWait:
    async def test_wait_returns_running(self, auth):
        call_count = 0
        client = AsyncSandboxClient(auth)

        # Mock get() directly on the client object.
        async def mock_get(name, **_):
            nonlocal call_count
            call_count += 1
            from vm_operator_sdk.models import Sandbox, SandboxStatus

            phase = SandboxPhase.RUNNING if call_count >= 2 else SandboxPhase.INITIALIZING
            sb = Sandbox(name=name, namespace="default")
            sb.status = SandboxStatus(phase=phase)
            return sb

        client.get = mock_get
        with patch("asyncio.sleep", new=AsyncMock()):
            sb = await client.wait_until_running("sb1", poll_interval=0.001)
        assert sb.phase == SandboxPhase.RUNNING

    async def test_wait_timeout_raises(self, auth):
        client = AsyncSandboxClient(auth)

        async def mock_get(name, **_):
            from vm_operator_sdk.models import Sandbox, SandboxStatus

            sb = Sandbox(name=name, namespace="default")
            sb.status = SandboxStatus(phase=SandboxPhase.PENDING)
            return sb

        client.get = mock_get
        with patch("asyncio.sleep", new=AsyncMock()):
            with pytest.raises(TimeoutError):
                await client.wait_until_running(
                    "sb1", timeout=0.001, poll_interval=0.001
                )

    async def test_wait_terminal_phase_raises(self, auth):
        client = AsyncSandboxClient(auth)

        async def mock_get(name, **_):
            from vm_operator_sdk.models import Sandbox, SandboxStatus

            sb = Sandbox(name=name, namespace="default")
            sb.status = SandboxStatus(phase=SandboxPhase.FAILED)
            return sb

        client.get = mock_get
        with pytest.raises(APIError):
            await client.wait_until_running("sb1")


@pytest.mark.asyncio
class TestAsyncSandboxClientBatch:
    async def test_create_batch_concurrent(self, auth):
        client = AsyncSandboxClient(auth)
        created_names = []

        async def mock_create(name, **_):
            from vm_operator_sdk.models import Sandbox

            created_names.append(name)
            return Sandbox(name=name, namespace="default")

        client.create = mock_create
        result = await client.create_batch(["sb1", "sb2", "sb3"])
        assert len(result) == 3
        assert set(created_names) == {"sb1", "sb2", "sb3"}

    async def test_delete_batch_ignores_not_found(self, auth):
        client = AsyncSandboxClient(auth)

        async def mock_delete(name, **_):
            raise SandboxNotFoundError(name, "default")

        client.delete = mock_delete
        # Should not raise
        await client.delete_batch(["sb1", "sb2"])

    async def test_delete_batch_raises_when_not_ignored(self, auth):
        client = AsyncSandboxClient(auth)

        async def mock_delete(name, **_):
            raise SandboxNotFoundError(name, "default")

        client.delete = mock_delete
        with pytest.raises(SandboxNotFoundError):
            await client.delete_batch(["sb1"], ignore_not_found=False)


@pytest.mark.asyncio
class TestAsyncSandboxClientContextManager:
    async def test_async_context_manager(self, auth):
        manifest = make_sandbox_manifest("sb1")
        client = _patched_client(auth, manifest)
        async with client as c:
            sb = await c.get("sb1")
        assert sb.name == "sb1"
