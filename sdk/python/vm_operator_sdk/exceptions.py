"""Custom exception types for vm-operator-sdk."""

from __future__ import annotations


class VMOperatorError(Exception):
    """Base exception for all vm-operator-sdk errors."""


class AuthenticationError(VMOperatorError):
    """Raised when authentication configuration is missing or invalid."""


class SandboxNotFoundError(VMOperatorError):
    """Raised when a Sandbox resource does not exist."""

    def __init__(self, name: str, namespace: str) -> None:
        self.name = name
        self.namespace = namespace
        super().__init__(f"Sandbox '{name}' not found in namespace '{namespace}'")


class SandboxAlreadyExistsError(VMOperatorError):
    """Raised when a Sandbox resource already exists."""

    def __init__(self, name: str, namespace: str) -> None:
        self.name = name
        self.namespace = namespace
        super().__init__(f"Sandbox '{name}' already exists in namespace '{namespace}'")


class APIError(VMOperatorError):
    """Raised when the Kubernetes API returns an unexpected HTTP status."""

    def __init__(self, status_code: int, message: str = "") -> None:
        self.status_code = status_code
        self.message = message
        super().__init__(f"API error {status_code}: {message}")


class TimeoutError(VMOperatorError):
    """Raised when a wait-for-ready operation times out."""


class InvalidSpecError(VMOperatorError):
    """Raised when an invalid Sandbox specification is provided."""
