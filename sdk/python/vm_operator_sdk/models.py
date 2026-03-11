"""Data models for Sandbox resources.

These classes mirror the Go types in ``api/v1alpha1/sandbox_types.go`` and are
used to construct and parse Kubernetes API payloads.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from enum import Enum
from typing import Any, Dict, List, Optional


class SandboxPhase(str, Enum):
    """Lifecycle phase of a Sandbox."""

    PENDING = "Pending"
    SCHEDULING = "Scheduling"
    INITIALIZING = "Initializing"
    RUNNING = "Running"
    PAUSING = "Pausing"
    PAUSED = "Paused"
    RESUMING = "Resuming"
    KILLING = "Killing"
    FAILED = "Failed"


class IsolationPolicy(str, Enum):
    """Network-policy isolation mode for a Sandbox's launcher Pod."""

    NONE = "None"
    DEFAULT = "Default"


# ---------------------------------------------------------------------------
# Spec sub-types
# ---------------------------------------------------------------------------


@dataclass
class ResourcesSpec:
    """Compute resources for the MicroVM."""

    vcpu: int = 2
    memory_mb: int = 512
    disk_mb: int = 2048
    huge_pages: bool = False

    def to_dict(self) -> Dict[str, Any]:
        return {
            "vcpu": self.vcpu,
            "memoryMB": self.memory_mb,
            "diskMB": self.disk_mb,
            "hugePages": self.huge_pages,
        }

    @classmethod
    def from_dict(cls, data: Dict[str, Any]) -> "ResourcesSpec":
        return cls(
            vcpu=data.get("vcpu", 2),
            memory_mb=data.get("memoryMB", 512),
            disk_mb=data.get("diskMB", 2048),
            huge_pages=data.get("hugePages", False),
        )


@dataclass
class TemplateSpec:
    """Template information for a Sandbox."""

    template_id: str = ""
    base_template_id: str = ""

    def to_dict(self) -> Dict[str, Any]:
        d: Dict[str, Any] = {"templateID": self.template_id}
        if self.base_template_id:
            d["baseTemplateID"] = self.base_template_id
        return d

    @classmethod
    def from_dict(cls, data: Dict[str, Any]) -> "TemplateSpec":
        return cls(
            template_id=data.get("templateID", ""),
            base_template_id=data.get("baseTemplateID", ""),
        )


@dataclass
class LifecycleSpec:
    """Lifecycle policies for a Sandbox."""

    timeout_seconds: int = 300

    def to_dict(self) -> Dict[str, Any]:
        return {"timeoutSeconds": self.timeout_seconds}

    @classmethod
    def from_dict(cls, data: Dict[str, Any]) -> "LifecycleSpec":
        return cls(timeout_seconds=data.get("timeoutSeconds", 300))


@dataclass
class SchedulingSpec:
    """Scheduling constraints for the launcher Pod."""

    node_selector: Dict[str, str] = field(default_factory=dict)
    node_name: str = ""

    def to_dict(self) -> Dict[str, Any]:
        d: Dict[str, Any] = {}
        if self.node_selector:
            d["nodeSelector"] = self.node_selector
        if self.node_name:
            d["nodeName"] = self.node_name
        return d

    @classmethod
    def from_dict(cls, data: Dict[str, Any]) -> "SchedulingSpec":
        return cls(
            node_selector=data.get("nodeSelector", {}),
            node_name=data.get("nodeName", ""),
        )


@dataclass
class NetworkPolicySpec:
    """Network isolation configuration for a Sandbox's launcher Pod."""

    isolation_policy: IsolationPolicy = IsolationPolicy.NONE

    def to_dict(self) -> Dict[str, Any]:
        return {"isolationPolicy": self.isolation_policy.value}

    @classmethod
    def from_dict(cls, data: Dict[str, Any]) -> "NetworkPolicySpec":
        policy = IsolationPolicy(data.get("isolationPolicy", "None"))
        return cls(isolation_policy=policy)


@dataclass
class RuntimeSpec:
    """Runtime configuration for the MicroVM."""

    kernel_version: str = ""
    firecracker_version: str = ""

    def to_dict(self) -> Dict[str, Any]:
        d: Dict[str, Any] = {}
        if self.kernel_version:
            d["kernelVersion"] = self.kernel_version
        if self.firecracker_version:
            d["firecrackerVersion"] = self.firecracker_version
        return d

    @classmethod
    def from_dict(cls, data: Dict[str, Any]) -> "RuntimeSpec":
        return cls(
            kernel_version=data.get("kernelVersion", ""),
            firecracker_version=data.get("firecrackerVersion", ""),
        )


# ---------------------------------------------------------------------------
# Top-level Sandbox types
# ---------------------------------------------------------------------------


@dataclass
class SandboxSpec:
    """Desired state of a Sandbox."""

    resources: ResourcesSpec = field(default_factory=ResourcesSpec)
    template: TemplateSpec = field(default_factory=TemplateSpec)
    lifecycle: LifecycleSpec = field(default_factory=LifecycleSpec)
    scheduling: SchedulingSpec = field(default_factory=SchedulingSpec)
    network_policy: NetworkPolicySpec = field(default_factory=NetworkPolicySpec)
    runtime: RuntimeSpec = field(default_factory=RuntimeSpec)
    sandbox_metadata: Dict[str, str] = field(default_factory=dict)
    paused: bool = False

    def to_dict(self) -> Dict[str, Any]:
        d: Dict[str, Any] = {
            "resources": self.resources.to_dict(),
            "template": self.template.to_dict(),
            "lifecycle": self.lifecycle.to_dict(),
            "networkPolicy": self.network_policy.to_dict(),
        }
        scheduling = self.scheduling.to_dict()
        if scheduling:
            d["scheduling"] = scheduling
        runtime = self.runtime.to_dict()
        if runtime:
            d["runtime"] = runtime
        if self.sandbox_metadata:
            d["sandboxMetadata"] = self.sandbox_metadata
        if self.paused:
            d["paused"] = self.paused
        return d

    @classmethod
    def from_dict(cls, data: Dict[str, Any]) -> "SandboxSpec":
        return cls(
            resources=ResourcesSpec.from_dict(data.get("resources", {})),
            template=TemplateSpec.from_dict(data.get("template", {})),
            lifecycle=LifecycleSpec.from_dict(data.get("lifecycle", {})),
            scheduling=SchedulingSpec.from_dict(data.get("scheduling", {})),
            network_policy=NetworkPolicySpec.from_dict(data.get("networkPolicy", {})),
            runtime=RuntimeSpec.from_dict(data.get("runtime", {})),
            sandbox_metadata=data.get("sandboxMetadata", {}),
            paused=data.get("paused", False),
        )


@dataclass
class SandboxStatus:
    """Observed state of a Sandbox."""

    phase: Optional[SandboxPhase] = None
    node_name: str = ""
    pod_name: str = ""
    start_time: Optional[str] = None
    end_time: Optional[str] = None
    snapshot_id: str = ""
    paused_at: Optional[str] = None
    conditions: List[Dict[str, Any]] = field(default_factory=list)
    observed_generation: int = 0

    @property
    def is_running(self) -> bool:
        """Return True when the Sandbox MicroVM is fully running."""
        return self.phase == SandboxPhase.RUNNING

    @property
    def is_terminal(self) -> bool:
        """Return True when the Sandbox has reached a terminal phase."""
        return self.phase in (SandboxPhase.FAILED, SandboxPhase.KILLING)

    @classmethod
    def from_dict(cls, data: Dict[str, Any]) -> "SandboxStatus":
        raw_phase = data.get("phase")
        phase = SandboxPhase(raw_phase) if raw_phase else None
        return cls(
            phase=phase,
            node_name=data.get("nodeName", ""),
            pod_name=data.get("podName", ""),
            start_time=data.get("startTime"),
            end_time=data.get("endTime"),
            snapshot_id=data.get("snapshotId", ""),
            paused_at=data.get("pausedAt"),
            conditions=data.get("conditions", []),
            observed_generation=data.get("observedGeneration", 0),
        )


@dataclass
class Sandbox:
    """Represents a ``sandbox.e2b.io/v1alpha1`` Sandbox resource."""

    name: str
    namespace: str
    spec: SandboxSpec = field(default_factory=SandboxSpec)
    status: Optional[SandboxStatus] = None
    labels: Dict[str, str] = field(default_factory=dict)
    annotations: Dict[str, str] = field(default_factory=dict)
    resource_version: str = ""

    @property
    def phase(self) -> Optional[SandboxPhase]:
        """Convenience accessor for ``status.phase``."""
        return self.status.phase if self.status else None

    def to_manifest(self) -> Dict[str, Any]:
        """Serialise to a Kubernetes API object suitable for POST/PUT."""
        manifest: Dict[str, Any] = {
            "apiVersion": "sandbox.e2b.io/v1alpha1",
            "kind": "Sandbox",
            "metadata": {
                "name": self.name,
                "namespace": self.namespace,
            },
            "spec": self.spec.to_dict(),
        }
        if self.labels:
            manifest["metadata"]["labels"] = self.labels
        if self.annotations:
            manifest["metadata"]["annotations"] = self.annotations
        return manifest

    @classmethod
    def from_manifest(cls, data: Dict[str, Any]) -> "Sandbox":
        """Deserialise from a raw Kubernetes API response."""
        meta = data.get("metadata", {})
        status_data = data.get("status", {})
        return cls(
            name=meta.get("name", ""),
            namespace=meta.get("namespace", ""),
            spec=SandboxSpec.from_dict(data.get("spec", {})),
            status=SandboxStatus.from_dict(status_data) if status_data else None,
            labels=meta.get("labels", {}),
            annotations=meta.get("annotations", {}),
            resource_version=meta.get("resourceVersion", ""),
        )
