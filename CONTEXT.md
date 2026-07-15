# ClawManager

Control plane for running AI agent instances (OpenClaw/Hermes) on Kubernetes: tenant, lifecycle, gateway access, model injection, and policy.

## Language

### Runtime

**Runtime Type**:
The workload shape an instance runs: `desktop` (full desktop streaming), `shell`, or `gateway` (headless Hermes/OpenClaw gateway process only). Answers "what runs inside".
_Avoid_: instance type (that is OpenClaw vs Hermes), workload type

**Instance Mode**:
The isolation and cost tier of an instance: `lite`, `isolated`, or `pro`. Answers "how strongly it is isolated and billed". Orthogonal to Runtime Type; the two are not derivable from each other.
_Avoid_: runtime mode, tier

**Lite**:
Instance Mode where multiple agent instances share one runtime Pod. High density, low cost, weak per-agent isolation.

**Isolated**:
Instance Mode where each instance gets its own Kubernetes isolation unit (Pod/Sandbox) with no desktop stack. Displayed as "Isolated Gateway" (独立网关) because it currently only combines with Runtime Type `gateway`.
_Avoid_: isolated_gateway, agent_pod, pod_gateway, sandbox (as a mode name)

**Pro**:
Instance Mode where each instance gets its own Pod with the desktop streaming stack (Webtop/KasmVNC/Selkies-style).

**Sandbox**:
The `kubernetes-sigs/agent-sandbox` CRD object — an implementation substrate an Isolated instance may run on. Never a product-facing mode name.
