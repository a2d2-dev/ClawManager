# agent-sandbox is a required dependency for Isolated mode

The Isolated instance mode (per-agent Kubernetes isolation unit, headless gateway only) runs exclusively on `kubernetes-sigs/agent-sandbox` CRDs via a single `sandboxBackend`. There is no native Pod/Service fallback backend: if the Sandbox CRD is not installed in the cluster, the control plane marks Isolated mode as unavailable with an explicit reason, and creation requests fail loudly.

## Considered Options

- **Dual backend** (issue #1's original proposal): `sandboxBackend` plus a minimal `nativePodBackend` chosen by cluster capability. Rejected: it is a silent-fallback pattern, doubles the test surface, and behavioral differences (suspend semantics, warm pools, PVC lifecycle) would leak into the product layer.
- **Native pods first, adopt agent-sandbox later**: rejected — re-implements lifecycle/PVC/warm-pool machinery upstream already provides (Apache-2.0, actively released).

## Consequences

- Dev clusters must install the agent-sandbox controller + CRDs (low cost: one controller).
- The `RuntimeBackend` interface still leaves room to add another backend if the agent-sandbox spike (issue #1 Phase 2) fails; that would supersede this ADR.
