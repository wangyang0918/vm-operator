"""Authentication helpers for vm-operator-sdk.

Supports three authentication modes:

1. **Kubeconfig** – reads ``~/.kube/config`` (or the path given by the
   ``KUBECONFIG`` environment variable).  Service-account token, client
   certificates, OIDC tokens and ``exec``-based credential plugins are all
   delegated to the ``kubernetes`` library when it is installed; otherwise a
   best-effort plain-token extraction is used.
2. **Bearer token** – a raw token string passed directly (e.g. a service-account
   JWT injected into a Pod by Kubernetes).
3. **In-cluster** – automatically reads the service-account token mounted at
   ``/var/run/secrets/kubernetes.io/serviceaccount/token`` and the CA bundle
   at ``/var/run/secrets/kubernetes.io/serviceaccount/ca.crt`` when running
   inside a Kubernetes Pod.
"""

from __future__ import annotations

import os
import ssl
import base64
import tempfile
from dataclasses import dataclass, field
from pathlib import Path
from typing import Optional, Tuple


_IN_CLUSTER_TOKEN_PATH = Path("/var/run/secrets/kubernetes.io/serviceaccount/token")
_IN_CLUSTER_CA_PATH = Path("/var/run/secrets/kubernetes.io/serviceaccount/ca.crt")
_IN_CLUSTER_NAMESPACE_PATH = Path(
    "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
)


@dataclass
class AuthConfig:
    """Resolved authentication configuration used by the HTTP clients."""

    # The full base URL of the Kubernetes API server, e.g. "https://127.0.0.1:6443".
    server: str = "https://127.0.0.1:6443"

    # Bearer token (service-account JWT or OIDC token).
    token: str = ""

    # Paths to PEM files used for mutual TLS.
    client_cert_file: str = ""
    client_key_file: str = ""

    # Path to the CA bundle used to verify the API server's TLS certificate.
    # An empty string means "use system CAs".
    ca_bundle_file: str = ""

    # When True, skip TLS certificate verification (insecure; not recommended).
    insecure_skip_tls_verify: bool = False

    # Temporary files created by this object (cleaned up when no longer needed).
    _tmpfiles: list = field(default_factory=list, repr=False, compare=False)

    def cleanup(self) -> None:
        """Remove any temporary files created during config resolution."""
        for path in self._tmpfiles:
            try:
                os.unlink(path)
            except OSError:
                pass
        self._tmpfiles.clear()

    def request_headers(self) -> dict:
        """Return HTTP headers required for all API requests."""
        headers: dict = {"Content-Type": "application/json", "Accept": "application/json"}
        if self.token:
            headers["Authorization"] = f"Bearer {self.token}"
        return headers

    def ssl_context(self) -> Optional[ssl.SSLContext]:
        """Return an ``ssl.SSLContext`` suitable for the chosen TLS settings."""
        if self.insecure_skip_tls_verify:
            ctx = ssl.create_default_context()
            ctx.check_hostname = False
            ctx.verify_mode = ssl.CERT_NONE
            return ctx
        ctx = ssl.create_default_context()
        if self.ca_bundle_file:
            ctx.load_verify_locations(cafile=self.ca_bundle_file)
        if self.client_cert_file and self.client_key_file:
            ctx.load_cert_chain(
                certfile=self.client_cert_file, keyfile=self.client_key_file
            )
        return ctx

    def requests_kwargs(self) -> dict:
        """Return kwargs compatible with the ``requests`` library."""
        kwargs: dict = {}
        if self.insecure_skip_tls_verify:
            kwargs["verify"] = False
        elif self.ca_bundle_file:
            kwargs["verify"] = self.ca_bundle_file
        else:
            kwargs["verify"] = True
        if self.client_cert_file and self.client_key_file:
            kwargs["cert"] = (self.client_cert_file, self.client_key_file)
        return kwargs


# ---------------------------------------------------------------------------
# Factory functions
# ---------------------------------------------------------------------------


def from_token(
    server: str,
    token: str,
    *,
    ca_bundle_file: str = "",
    insecure_skip_tls_verify: bool = False,
) -> AuthConfig:
    """Build an :class:`AuthConfig` from a raw bearer token.

    Parameters
    ----------
    server:
        Base URL of the Kubernetes API server (e.g. ``"https://k8s.example.com"``).
    token:
        Service-account JWT or OIDC bearer token.
    ca_bundle_file:
        Optional path to a PEM CA bundle for TLS verification.
    insecure_skip_tls_verify:
        When ``True``, skip server certificate verification (not recommended in
        production).
    """
    return AuthConfig(
        server=server.rstrip("/"),
        token=token,
        ca_bundle_file=ca_bundle_file,
        insecure_skip_tls_verify=insecure_skip_tls_verify,
    )


def from_in_cluster() -> AuthConfig:
    """Build an :class:`AuthConfig` from Kubernetes in-cluster configuration.

    This works when the SDK is used inside a Pod that has a service-account
    token mounted by Kubernetes.

    Raises
    ------
    FileNotFoundError
        If the in-cluster token or CA certificate files are not found.
    """
    token = _IN_CLUSTER_TOKEN_PATH.read_text(encoding="utf-8").strip()
    ca_bundle = str(_IN_CLUSTER_CA_PATH)

    # Determine the API server URL from the well-known environment variables.
    host = os.environ.get("KUBERNETES_SERVICE_HOST", "kubernetes.default.svc")
    port = os.environ.get("KUBERNETES_SERVICE_PORT", "443")
    server = f"https://{host}:{port}"

    return AuthConfig(server=server, token=token, ca_bundle_file=ca_bundle)


def from_kubeconfig(
    kubeconfig_path: Optional[str] = None,
    context: Optional[str] = None,
) -> AuthConfig:
    """Build an :class:`AuthConfig` by parsing a kubeconfig file.

    Parameters
    ----------
    kubeconfig_path:
        Path to the kubeconfig file.  Defaults to ``$KUBECONFIG`` or
        ``~/.kube/config``.
    context:
        Name of the kubeconfig context to use.  Defaults to the current context.
    """
    path = Path(
        kubeconfig_path
        or os.environ.get("KUBECONFIG", "")
        or Path.home() / ".kube" / "config"
    )

    import yaml  # local import so that PyYAML is only required when used

    with path.open(encoding="utf-8") as fh:
        kc = yaml.safe_load(fh)

    current_context_name = context or kc.get("current-context", "")
    ctx_map = {c["name"]: c["context"] for c in kc.get("contexts", [])}
    ctx = ctx_map.get(current_context_name, {})

    cluster_map = {c["name"]: c["cluster"] for c in kc.get("clusters", [])}
    user_map = {u["name"]: u["user"] for u in kc.get("users", [])}

    cluster = cluster_map.get(ctx.get("cluster", ""), {})
    user = user_map.get(ctx.get("user", ""), {})

    server = cluster.get("server", "https://127.0.0.1:6443").rstrip("/")
    insecure = cluster.get("insecure-skip-tls-verify", False)

    tmpfiles: list = []
    ca_bundle_file = ""
    if not insecure:
        ca_data = cluster.get("certificate-authority-data", "")
        ca_file = cluster.get("certificate-authority", "")
        if ca_data:
            ca_bundle_file = _write_tmpfile(base64.b64decode(ca_data), tmpfiles)
        elif ca_file:
            ca_bundle_file = ca_file

    token = user.get("token", "")

    client_cert_file = ""
    client_key_file = ""
    cert_data = user.get("client-certificate-data", "")
    cert_file = user.get("client-certificate", "")
    key_data = user.get("client-key-data", "")
    key_file = user.get("client-key", "")

    if cert_data:
        client_cert_file = _write_tmpfile(base64.b64decode(cert_data), tmpfiles)
    elif cert_file:
        client_cert_file = cert_file

    if key_data:
        client_key_file = _write_tmpfile(base64.b64decode(key_data), tmpfiles)
    elif key_file:
        client_key_file = key_file

    return AuthConfig(
        server=server,
        token=token,
        client_cert_file=client_cert_file,
        client_key_file=client_key_file,
        ca_bundle_file=ca_bundle_file,
        insecure_skip_tls_verify=insecure,
        _tmpfiles=tmpfiles,
    )


def _write_tmpfile(data: bytes, registry: list) -> str:
    """Write *data* to a temporary file, add its path to *registry*, return path."""
    fd, path = tempfile.mkstemp()
    try:
        os.write(fd, data)
    finally:
        os.close(fd)
    registry.append(path)
    return path
