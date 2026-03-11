"""Unit tests for vm_operator_sdk.models."""

from __future__ import annotations

import pytest

from vm_operator_sdk.models import (
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


# ---------------------------------------------------------------------------
# ResourcesSpec
# ---------------------------------------------------------------------------


class TestResourcesSpec:
    def test_defaults(self):
        r = ResourcesSpec()
        assert r.vcpu == 2
        assert r.memory_mb == 512
        assert r.disk_mb == 2048
        assert r.huge_pages is False

    def test_to_dict(self):
        r = ResourcesSpec(vcpu=4, memory_mb=1024)
        d = r.to_dict()
        assert d["vcpu"] == 4
        assert d["memoryMB"] == 1024
        assert d["diskMB"] == 2048
        assert d["hugePages"] is False

    def test_from_dict_roundtrip(self):
        original = ResourcesSpec(vcpu=8, memory_mb=2048, disk_mb=4096, huge_pages=True)
        restored = ResourcesSpec.from_dict(original.to_dict())
        assert original == restored

    def test_from_dict_with_missing_keys(self):
        r = ResourcesSpec.from_dict({})
        assert r.vcpu == 2
        assert r.memory_mb == 512


# ---------------------------------------------------------------------------
# TemplateSpec
# ---------------------------------------------------------------------------


class TestTemplateSpec:
    def test_to_dict_without_base(self):
        t = TemplateSpec(template_id="ubuntu-22.04")
        d = t.to_dict()
        assert d["templateID"] == "ubuntu-22.04"
        assert "baseTemplateID" not in d

    def test_to_dict_with_base(self):
        t = TemplateSpec(template_id="custom", base_template_id="ubuntu-22.04")
        d = t.to_dict()
        assert d["baseTemplateID"] == "ubuntu-22.04"

    def test_from_dict_roundtrip(self):
        original = TemplateSpec(template_id="t1", base_template_id="base")
        assert TemplateSpec.from_dict(original.to_dict()) == original


# ---------------------------------------------------------------------------
# LifecycleSpec
# ---------------------------------------------------------------------------


class TestLifecycleSpec:
    def test_default_timeout(self):
        assert LifecycleSpec().timeout_seconds == 300

    def test_roundtrip(self):
        original = LifecycleSpec(timeout_seconds=600)
        assert LifecycleSpec.from_dict(original.to_dict()) == original


# ---------------------------------------------------------------------------
# SchedulingSpec
# ---------------------------------------------------------------------------


class TestSchedulingSpec:
    def test_to_dict_empty(self):
        assert SchedulingSpec().to_dict() == {}

    def test_to_dict_with_node_selector(self):
        s = SchedulingSpec(node_selector={"kvm": "true"})
        d = s.to_dict()
        assert d["nodeSelector"] == {"kvm": "true"}
        assert "nodeName" not in d

    def test_to_dict_with_node_name(self):
        s = SchedulingSpec(node_name="worker-1")
        assert s.to_dict()["nodeName"] == "worker-1"

    def test_roundtrip(self):
        original = SchedulingSpec(node_selector={"a": "b"}, node_name="n1")
        assert SchedulingSpec.from_dict(original.to_dict()) == original


# ---------------------------------------------------------------------------
# NetworkPolicySpec
# ---------------------------------------------------------------------------


class TestNetworkPolicySpec:
    def test_default_none(self):
        assert NetworkPolicySpec().isolation_policy == IsolationPolicy.NONE

    def test_default_policy(self):
        n = NetworkPolicySpec(isolation_policy=IsolationPolicy.DEFAULT)
        assert n.to_dict()["isolationPolicy"] == "Default"

    def test_roundtrip(self):
        original = NetworkPolicySpec(isolation_policy=IsolationPolicy.DEFAULT)
        assert NetworkPolicySpec.from_dict(original.to_dict()) == original


# ---------------------------------------------------------------------------
# RuntimeSpec
# ---------------------------------------------------------------------------


class TestRuntimeSpec:
    def test_to_dict_empty(self):
        assert RuntimeSpec().to_dict() == {}

    def test_to_dict_with_kernel(self):
        r = RuntimeSpec(kernel_version="5.10", firecracker_version="1.4")
        d = r.to_dict()
        assert d["kernelVersion"] == "5.10"
        assert d["firecrackerVersion"] == "1.4"


# ---------------------------------------------------------------------------
# SandboxSpec
# ---------------------------------------------------------------------------


class TestSandboxSpec:
    def test_to_dict_has_required_keys(self):
        d = SandboxSpec().to_dict()
        assert "resources" in d
        assert "template" in d
        assert "lifecycle" in d
        assert "networkPolicy" in d

    def test_paused_only_emitted_when_true(self):
        assert "paused" not in SandboxSpec(paused=False).to_dict()
        assert SandboxSpec(paused=True).to_dict()["paused"] is True

    def test_roundtrip(self):
        original = SandboxSpec(
            resources=ResourcesSpec(vcpu=4, memory_mb=1024),
            template=TemplateSpec(template_id="ubuntu"),
            lifecycle=LifecycleSpec(timeout_seconds=60),
            scheduling=SchedulingSpec(node_name="node-1"),
            paused=True,
        )
        restored = SandboxSpec.from_dict(original.to_dict())
        assert restored.resources.vcpu == 4
        assert restored.template.template_id == "ubuntu"
        assert restored.lifecycle.timeout_seconds == 60
        assert restored.scheduling.node_name == "node-1"
        assert restored.paused is True


# ---------------------------------------------------------------------------
# SandboxStatus
# ---------------------------------------------------------------------------


class TestSandboxStatus:
    def test_from_dict_empty(self):
        s = SandboxStatus.from_dict({})
        assert s.phase is None
        assert s.node_name == ""

    def test_from_dict_with_phase(self):
        s = SandboxStatus.from_dict({"phase": "Running"})
        assert s.phase == SandboxPhase.RUNNING

    def test_is_running(self):
        s = SandboxStatus.from_dict({"phase": "Running"})
        assert s.is_running is True

    def test_is_not_running(self):
        s = SandboxStatus.from_dict({"phase": "Pending"})
        assert s.is_running is False

    def test_is_terminal_failed(self):
        s = SandboxStatus.from_dict({"phase": "Failed"})
        assert s.is_terminal is True

    def test_is_not_terminal(self):
        s = SandboxStatus.from_dict({"phase": "Running"})
        assert s.is_terminal is False


# ---------------------------------------------------------------------------
# Sandbox
# ---------------------------------------------------------------------------


class TestSandbox:
    def test_to_manifest_contains_api_version(self):
        sb = Sandbox(name="test", namespace="default")
        m = sb.to_manifest()
        assert m["apiVersion"] == "sandbox.e2b.io/v1alpha1"
        assert m["kind"] == "Sandbox"
        assert m["metadata"]["name"] == "test"

    def test_to_manifest_no_empty_labels(self):
        sb = Sandbox(name="test", namespace="default")
        assert "labels" not in sb.to_manifest()["metadata"]

    def test_to_manifest_includes_labels(self):
        sb = Sandbox(name="test", namespace="default", labels={"env": "dev"})
        assert sb.to_manifest()["metadata"]["labels"] == {"env": "dev"}

    def test_from_manifest_roundtrip(self):
        sb = Sandbox(
            name="my-sandbox",
            namespace="production",
            spec=SandboxSpec(
                resources=ResourcesSpec(vcpu=4),
                template=TemplateSpec(template_id="ubuntu-22.04"),
            ),
            labels={"team": "platform"},
        )
        # Status is excluded from the manifest; only spec/metadata survive.
        restored = Sandbox.from_manifest(sb.to_manifest())
        assert restored.name == "my-sandbox"
        assert restored.namespace == "production"
        assert restored.spec.resources.vcpu == 4
        assert restored.labels == {"team": "platform"}

    def test_phase_property_none_when_no_status(self):
        sb = Sandbox(name="x", namespace="default")
        assert sb.phase is None

    def test_phase_property_from_status(self):
        sb = Sandbox(name="x", namespace="default")
        sb.status = SandboxStatus(phase=SandboxPhase.RUNNING)
        assert sb.phase == SandboxPhase.RUNNING
