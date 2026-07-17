# RFC: Isolated Instance Mode

> **Status (2026-07-17)**: a complete reference implementation of this RFC has been built and merged on the fork `a2d2-dev/ClawManager` — the RuntimeBackend refactor (PRs #15, #18), the three-value instance mode with capability gating and UI (PR #20), the `sandboxBackend` on agent-sandbox v0.5.1 (PR #21), gateway access and an e2e spec (PR #24). The agent-sandbox substrate was validated empirically on a live cluster (fork issue #3): the `Suspended` operating mode frees the Pod while preserving the PVC and its data, `Finished(PodFailed)` is not auto-recovered upstream (kubernetes-sigs/agent-sandbox#729), the PVC's ownerReference points at the Sandbox CR (so CR deletion cascades), and stale conditions can coexist with current ones. Every finding is reflected in the implementation. Section 3 remains the decision requested from upstream maintainers.

## 1. Motivation

ClawManager currently has a useful but incomplete instance-mode split for headless agent workloads:

- **Lite mode** is cost-efficient and fast because it places many instances in a shared runtime Pod. Each Lite instance is a gateway subprocess with its own workspace and gateway port, managed by the runtime-pod agent.
- **Pro mode** gives each instance its own Kubernetes workload, Service, and persistent workspace. It is the right shape for desktop workloads, but it also brings the desktop runtime stack along with the instance.

That leaves a gap for CI-style coding agents, Hermes/OpenClaw gateway processes, browserless automation, and other headless tool-execution workloads. These workloads want a per-instance Kubernetes isolation unit, resource limits, persistent workspace, and ClawManager gateway access, but they do not need a desktop, VNC/Webtop-style access path, or desktop-specific process supervision.

This RFC proposes an **Isolated** instance mode to fill that gap:

- one Kubernetes isolation unit per ClawManager instance;
- one headless Hermes/OpenClaw gateway process per unit;
- no desktop stack;
- persistent workspace across stop/start;
- access only through the existing ClawManager gateway/proxy and token model.

The intended user-facing tradeoff becomes:

| Mode | Isolation/cost shape | Primary workload |
| --- | --- | --- |
| `lite` | Many instances share a runtime Pod | Low-cost gateway instances |
| `isolated` | One headless sandbox per instance | Headless agents needing Kubernetes-level isolation |
| `pro` | One desktop runtime per instance | Full desktop/webtop instances |

## 2. Proposal

### Instance mode model

Introduce `instance_mode` as a three-value enum:

- `lite`
- `isolated`
- `pro`

Keep workload shape separate from isolation/cost tier. In current ClawManager terms, `runtime_type` continues to describe what is being run, such as `gateway`, `desktop`, or `shell`, while `instance_mode` describes how isolated and expensive the instance is. The first version of Isolated mode should only support headless gateway runtimes.

The practical outcome is that code should not infer mode from runtime type or runtime type from mode. Call sites should pass the requested mode explicitly.

### RuntimeBackend interface

Move lifecycle dispatch behind a common `RuntimeBackend` interface for all modes, including Lite and Pro. The first PR should be a behavior-preserving refactor: existing Lite and Pro behavior should continue to work exactly as before, but their lifecycle operations should be routed through backend implementations instead of scattered mode-specific branches.

A concrete interface can be adjusted during implementation, but the backend boundary should cover at least:

```go
type RuntimeBackend interface {
    Create(ctx context.Context, spec RuntimeCreateSpec) (*RuntimeInstance, error)
    Start(ctx context.Context, id RuntimeInstanceRef) error
    Stop(ctx context.Context, id RuntimeInstanceRef) error
    Delete(ctx context.Context, id RuntimeInstanceRef) error
    GetStatus(ctx context.Context, id RuntimeInstanceRef) (*RuntimeStatus, error)
    GetEndpoint(ctx context.Context, id RuntimeInstanceRef) (*RuntimeEndpoint, error)
    AttachPolicy(ctx context.Context, id RuntimeInstanceRef, policy RuntimePolicyRef) error
}
```

Initial implementations:

- `liteBackend`: existing shared runtime-pod gateway behavior.
- `proDesktopBackend`: existing dedicated desktop runtime behavior.
- `sandboxBackend`: new Isolated mode behavior backed by agent-sandbox.

`AttachPolicy` is included as a future attachment point, but this RFC does not propose a full policy model.

### sandboxBackend abstraction

The new `sandboxBackend` would create and reconcile an agent-sandbox `Sandbox` object for each Isolated instance. The backend should translate ClawManager instance data into the Sandbox pod template, including:

- runtime image and gateway command;
- ClawManager gateway/model-access environment injection;
- per-instance workspace PVC template;
- CPU/memory/storage limits;
- labels/annotations that let operators correlate a ClawManager instance with the Sandbox and backing Pod;
- optional Kubernetes placement fields such as `nodeSelector`, `runtimeClassName`, and `tolerations`, passed through without inventing a ClawManager-specific node-group model.

agent-sandbox is a reasonable substrate to evaluate because its README describes a Kubernetes `Sandbox` CRD for isolated, stateful singleton workloads with stable identity, persistent storage, lifecycle management, and persistent volume support:

- https://github.com/kubernetes-sigs/agent-sandbox/blob/main/README.md
- https://github.com/kubernetes-sigs/agent-sandbox/blob/main/docs/api.md

The same upstream docs also describe extension CRDs:

- `SandboxTemplate`
- `SandboxClaim`
- `SandboxWarmPool`

The initial Isolated mode does not need to consume those extension CRDs. It can start with direct `Sandbox` objects and leave warm-pool allocation for a later phase.

For stronger runtime isolation, the Sandbox pod template can use Kubernetes `runtimeClassName` when the cluster provides a RuntimeClass such as gVisor or Kata. agent-sandbox's OpenClaw example documents gVisor use and notes that the manifest can be adjusted for non-default runtime classes such as Kata Containers:

- https://github.com/kubernetes-sigs/agent-sandbox/blob/main/examples/openclaw-sandbox/README.md

This RFC does not claim that ClawManager should install or manage gVisor/Kata. It only proposes passing Kubernetes runtime-class placement through to the Sandbox pod template.

## 3. Core Point of Contention: Should agent-sandbox Be Required for Isolated Mode?

This is the main decision requested from maintainers.

The proposal is: **Isolated mode depends on agent-sandbox CRDs. There is no native-Pod fallback. If the required CRDs are not installed, Isolated mode is unavailable and create requests fail with a clear error.**

The rationale, following the ADR-0001 direction in the fork where this RFC originated, is:

- Isolated mode is being introduced specifically to provide a stronger per-instance isolation contract than Lite without carrying the desktop stack of Pro.
- A native-Pod fallback would look available in the UI/API while silently weakening the isolation substrate and lifecycle semantics.
- A single substrate keeps lifecycle mapping, status reconciliation, persistence, and future policy attachment easier to reason about.
- Cluster administrators get an explicit capability gate: install agent-sandbox to enable Isolated mode; otherwise the mode is reported unavailable.

That said, this is not presented as a settled upstream decision. I am explicitly asking maintainers to decide whether this mandatory dependency is acceptable for ClawManager.

If maintainers prefer a native-Pod fallback, that should be treated as a change to the design, not as a hidden implementation detail. In that case the RFC should be revised to define the fallback's isolation contract, lifecycle behavior, status mapping, tests, and user-facing availability semantics.

## 4. Phased PR Plan

### PR 1: RuntimeBackend refactor with no behavior change

- Add the `RuntimeBackend` interface and backend registry/dispatch.
- Move existing Lite lifecycle logic into `liteBackend`.
- Move existing Pro desktop lifecycle logic into `proDesktopBackend`.
- Preserve current API, UI, database behavior, and deployment behavior.
- Keep tests focused on proving Lite and Pro behavior did not change.

This PR should be reviewable as a pure refactor.

### PR 2: Isolated mode and sandboxBackend

- Add `instance_mode=isolated` to the backend domain model.
- Add capability detection for the agent-sandbox CRDs.
- Add `sandboxBackend`.
- Create one agent-sandbox `Sandbox` per Isolated instance.
- Map start/stop/delete/status/endpoint to Sandbox behavior.
- Persist enough references to correlate ClawManager instances with Sandbox objects.
- Return explicit unavailable errors when the CRDs are missing.

This PR should avoid UI redesign and keep the surface area as small as possible.

### PR 3: UI/API surface

- Add the three-way mode selector: Lite / Isolated / Pro.
- Show Isolated mode as unavailable when the cluster lacks the required CRDs.
- Hide desktop-only actions for Isolated instances.
- Show clear runtime/backend information on the instance detail page.
- Add documentation and user-facing error text.

## 5. Deliberately Deferred Roadmap

The following items are intentionally out of scope for this RFC:

- **Warm pool support.** agent-sandbox documents `SandboxWarmPool`, `SandboxTemplate`, and `SandboxClaim`, and the roadmap discusses additional warm-pool selection work. Isolated mode should first land with direct Sandbox creation; warm-pool allocation can follow once cold-start latency and capacity pressure are measured.
- **Egress policy.** agent-sandbox API docs include template-level network policy management, and the roadmap lists claim-time network-policy attachment as planned work. This RFC only keeps an `AttachPolicy` backend hook for future design; it does not define ClawManager egress policy, FQDN rules, Cilium integration, audit events, or blocked-traffic UX.
- **Pro migration to agent-sandbox.** The refactor should move current Pro lifecycle code behind `proDesktopBackend`, but this RFC does not propose converting Pro desktop instances to agent-sandbox. Pro should remain behavior-compatible while Isolated mode is evaluated separately.

