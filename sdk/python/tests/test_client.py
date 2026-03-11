"""Unit tests for vm_operator_sdk.client (SandboxClient).

All outbound HTTP calls are intercepted with ``unittest.mock`` so no real
Kubernetes cluster is needed.
"""

from __future__ import annotations

import json
import urllib.error
import urllib.request
from io import BytesIO
from typing import Any, Dict
from unittest.mock import MagicMock, patch

import pytest

from vm_operator_sdk.client import SandboxClient
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


def _fake_response(body: Dict[str, Any], status: int = 200) -> MagicMock:
    """Return a mock object that behaves like a urllib response."""
    raw = json.dumps(body).encode()
    resp = MagicMock()
    resp.__enter__ = lambda s: s
    resp.__exit__ = MagicMock(return_value=False)
    resp.read = MagicMock(return_value=raw)
    resp.status = status
    return resp


def _fake_http_error(status: int, body: str = "") -> urllib.error.HTTPError:
    return urllib.error.HTTPError(
        url="https://fake",
        code=status,
        msg="",
        hdrs=None,  # type: ignore[arg-type]
        fp=BytesIO(body.encode()),
    )


# ---------------------------------------------------------------------------
# Tests
# ---------------------------------------------------------------------------


class TestSandboxClientCreate:
    def test_create_returns_sandbox(self, auth):
        manifest = make_sandbox_manifest("sb1")
        with patch("urllib.request.urlopen", return_value=_fake_response(manifest)):
            client = SandboxClient(auth)
            sb = client.create("sb1", template_id="ubuntu-22.04")
        assert sb.name == "sb1"
        assert sb.namespace == "default"

    def test_create_uses_custom_namespace(self, auth):
        manifest = make_sandbox_manifest("sb1", namespace="prod")
        with patch("urllib.request.urlopen", return_value=_fake_response(manifest)):
            client = SandboxClient(auth, default_namespace="prod")
            sb = client.create("sb1")
        assert sb.namespace == "prod"

    def test_create_already_exists_raises(self, auth):
        err = _fake_http_error(409, json.dumps({"message": "already exists"}))
        with patch("urllib.request.urlopen", side_effect=err):
            client = SandboxClient(auth)
            with pytest.raises(SandboxAlreadyExistsError):
                client.create("sb1")

    def test_create_api_error_raises(self, auth):
        err = _fake_http_error(500, json.dumps({"message": "internal"}))
        with patch("urllib.request.urlopen", side_effect=err):
            client = SandboxClient(auth)
            with pytest.raises(APIError) as exc_info:
                client.create("sb1")
            assert exc_info.value.status_code == 500


class TestSandboxClientGet:
    def test_get_returns_sandbox(self, auth):
        manifest = make_sandbox_manifest("sb1", phase="Running")
        with patch("urllib.request.urlopen", return_value=_fake_response(manifest)):
            sb = SandboxClient(auth).get("sb1")
        assert sb.phase == SandboxPhase.RUNNING

    def test_get_not_found_raises(self, auth):
        err = _fake_http_error(404)
        with patch("urllib.request.urlopen", side_effect=err):
            with pytest.raises(SandboxNotFoundError) as exc_info:
                SandboxClient(auth).get("missing")
            assert exc_info.value.name == "missing"


class TestSandboxClientList:
    def test_list_empty(self, auth):
        raw = make_list_manifest([])
        with patch("urllib.request.urlopen", return_value=_fake_response(raw)):
            result = SandboxClient(auth).list()
        assert result == []

    def test_list_multiple(self, auth):
        raw = make_list_manifest(
            [make_sandbox_manifest("sb1"), make_sandbox_manifest("sb2")]
        )
        with patch("urllib.request.urlopen", return_value=_fake_response(raw)):
            result = SandboxClient(auth).list()
        assert len(result) == 2
        assert {sb.name for sb in result} == {"sb1", "sb2"}


class TestSandboxClientDelete:
    def test_delete_ok(self, auth):
        with patch("urllib.request.urlopen", return_value=_fake_response({})):
            SandboxClient(auth).delete("sb1")  # Should not raise

    def test_delete_not_found_raises(self, auth):
        err = _fake_http_error(404)
        with patch("urllib.request.urlopen", side_effect=err):
            with pytest.raises(SandboxNotFoundError):
                SandboxClient(auth).delete("missing")


class TestSandboxClientPauseResume:
    def test_pause_sets_paused_true(self, auth):
        manifest = make_sandbox_manifest("sb1", phase="Pausing", paused=True)
        captured_body = {}

        original_urlopen = urllib.request.urlopen

        def mock_urlopen(req, **kwargs):
            if isinstance(req, urllib.request.Request) and req.data:
                captured_body.update(json.loads(req.data))
            return _fake_response(manifest)

        with patch("urllib.request.urlopen", side_effect=mock_urlopen):
            sb = SandboxClient(auth).pause("sb1")
        assert captured_body.get("spec", {}).get("paused") is True

    def test_resume_sets_paused_false(self, auth):
        manifest = make_sandbox_manifest("sb1", phase="Resuming")
        captured_body = {}

        def mock_urlopen(req, **kwargs):
            if isinstance(req, urllib.request.Request) and req.data:
                captured_body.update(json.loads(req.data))
            return _fake_response(manifest)

        with patch("urllib.request.urlopen", side_effect=mock_urlopen):
            SandboxClient(auth).resume("sb1")
        assert captured_body.get("spec", {}).get("paused") is False


class TestSandboxClientWaitUntilRunning:
    def test_wait_returns_running_sandbox(self, auth):
        call_count = 0

        def mock_urlopen(req, **kwargs):
            nonlocal call_count
            call_count += 1
            phase = "Running" if call_count >= 2 else "Initializing"
            return _fake_response(make_sandbox_manifest("sb1", phase=phase))

        with patch("urllib.request.urlopen", side_effect=mock_urlopen):
            with patch("time.sleep"):  # skip actual sleeping
                sb = SandboxClient(auth).wait_until_running("sb1", poll_interval=0.001)
        assert sb.phase == SandboxPhase.RUNNING

    def test_wait_timeout_raises(self, auth):
        with patch(
            "urllib.request.urlopen",
            return_value=_fake_response(make_sandbox_manifest("sb1", phase="Pending")),
        ):
            with patch("time.sleep"):
                with pytest.raises(TimeoutError):
                    SandboxClient(auth).wait_until_running(
                        "sb1", timeout=0.001, poll_interval=0.001
                    )

    def test_wait_terminal_phase_raises(self, auth):
        with patch(
            "urllib.request.urlopen",
            return_value=_fake_response(make_sandbox_manifest("sb1", phase="Failed")),
        ):
            with pytest.raises(APIError):
                SandboxClient(auth).wait_until_running("sb1")


class TestSandboxClientBatch:
    def test_create_batch(self, auth):
        def mock_urlopen(req, **kwargs):
            data = json.loads(req.data)
            name = data["metadata"]["name"]
            return _fake_response(make_sandbox_manifest(name))

        with patch("urllib.request.urlopen", side_effect=mock_urlopen):
            sandboxes = SandboxClient(auth).create_batch(["sb1", "sb2", "sb3"])
        assert len(sandboxes) == 3

    def test_delete_batch_ignores_not_found(self, auth):
        err = _fake_http_error(404)
        with patch("urllib.request.urlopen", side_effect=err):
            # Should not raise because ignore_not_found=True by default
            SandboxClient(auth).delete_batch(["sb1", "sb2"])

    def test_delete_batch_raises_when_not_ignored(self, auth):
        err = _fake_http_error(404)
        with patch("urllib.request.urlopen", side_effect=err):
            with pytest.raises(SandboxNotFoundError):
                SandboxClient(auth).delete_batch(["sb1"], ignore_not_found=False)


class TestSandboxClientContextManager:
    def test_context_manager(self, auth):
        manifest = make_sandbox_manifest("sb1")
        with patch("urllib.request.urlopen", return_value=_fake_response(manifest)):
            with SandboxClient(auth) as client:
                sb = client.get("sb1")
        assert sb.name == "sb1"
