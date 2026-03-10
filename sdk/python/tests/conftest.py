"""Shared fixtures and helpers for vm-operator-sdk tests."""

from __future__ import annotations

import json
from typing import Any, Dict, List, Optional
from unittest.mock import MagicMock, patch

import pytest

from vm_operator_sdk.auth import AuthConfig
from vm_operator_sdk.models import SandboxPhase


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------


@pytest.fixture
def auth() -> AuthConfig:
    """Return a minimal AuthConfig suitable for unit tests."""
    return AuthConfig(
        server="https://fake-k8s-server:6443",
        token="test-token",
        insecure_skip_tls_verify=True,
    )


# ---------------------------------------------------------------------------
# Sandbox manifest builders
# ---------------------------------------------------------------------------


def make_sandbox_manifest(
    name: str = "test-sandbox",
    namespace: str = "default",
    phase: Optional[str] = None,
    paused: bool = False,
) -> Dict[str, Any]:
    """Return a minimal raw Kubernetes Sandbox manifest."""
    manifest: Dict[str, Any] = {
        "apiVersion": "sandbox.e2b.io/v1alpha1",
        "kind": "Sandbox",
        "metadata": {
            "name": name,
            "namespace": namespace,
            "resourceVersion": "1234",
        },
        "spec": {
            "resources": {"vcpu": 2, "memoryMB": 512, "diskMB": 2048},
            "template": {"templateID": "ubuntu-22.04"},
            "lifecycle": {"timeoutSeconds": 300},
            "networkPolicy": {"isolationPolicy": "None"},
            "paused": paused,
        },
        "status": {},
    }
    if phase:
        manifest["status"]["phase"] = phase
    return manifest


def make_list_manifest(items: List[Dict[str, Any]]) -> Dict[str, Any]:
    """Return a raw Kubernetes list manifest containing *items*."""
    return {
        "apiVersion": "sandbox.e2b.io/v1alpha1",
        "kind": "SandboxList",
        "metadata": {},
        "items": items,
    }
