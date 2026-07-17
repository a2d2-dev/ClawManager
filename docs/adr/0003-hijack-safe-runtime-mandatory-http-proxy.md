# Isolated tier enforces HTTP_PROXY; lite and pro do not

The `isolated` instance mode (see ADR-0001) requires that all outbound HTTP/HTTPS traffic from the gateway subprocess flows through the platform egress proxy — HTTP_PROXY and HTTPS_PROXY are injected and treated as mandatory, and the image is expected to fail closed when the proxy is unreachable. `lite` and `pro` leave HTTP_PROXY as an optional injection.

> **Status note (v1 tension)**: The Isolated Instance Mode RFC (`.worktrees/issue-4-rfc/docs/rfc/isolated-instance-mode.md` §5) defers egress policy entirely and only reserves an `AttachPolicy` backend hook. This ADR states the eventual product-level requirement. Reconciling the two — whether HTTP_PROXY enforcement lands in PR2 alongside `sandboxBackend`, or defers to a follow-on PR — is one of the open decisions this wayfinder map should resolve.

## Considered Options

- **K8s NetworkPolicy egress rules** — rejected as the primary mechanism because it requires the customer cluster's CNI to enforce egress policies, which is not portable across the range of edge environments we deploy to.
- **eBPF-based outbound interception** — rejected for the same portability reason plus operational complexity.

HTTP_PROXY was chosen because it is a runtime-image-level control the platform can guarantee regardless of the underlying cluster infrastructure; if the runtime image respects it and fails closed on proxy failure, the guarantee holds.

## Consequences

- The Hijack-safe runtime image must be built to honor HTTP_PROXY without bypass paths (no direct socket libraries that ignore the env, no fallback to direct connection when the proxy errors).
- Any tool or SDK bundled into the Hijack-safe image must be audited for proxy-honoring behavior; this is a real image-level burden that Lite and Pro do not carry.
