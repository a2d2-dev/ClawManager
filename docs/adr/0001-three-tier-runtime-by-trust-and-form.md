# Three-tier runtime by trust model × interaction form

ClawManager offers Agent runtimes as three parallel tiers — Lite (shared-Pod, web-only), Pro (dedicated-Pod, Webtop desktop), and Isolated (dedicated-Pod, mandatory outbound proxy, hardened image) — picked at instance creation, never upgraded across tiers to satisfy a stronger requirement. The two axes are orthogonal: interaction form (web / desktop / hardened) and trust model (correctness-level / Pod-boundary / hijack-level).

## Considered Options

- **Single runtime with configurable "modes"** — rejected because the density model that makes Lite cheap (up to ~100 gateway subprocesses per Runtime Pod, sharing network namespace and filesystem view) is structurally incompatible with hijack-level isolation (one Pod per instance, no shared kernel surface reachable by peers). Merging them yields a runtime that is either too expensive for Lite users or too weak for Isolated users.
- **Two tiers only (Lite + Pro)** — rejected because Pro's isolation guarantee is "K8s Pod boundary + trusted user", which is insufficient once we assume the Agent will be hijacked by prompt injection from external content.

## Consequences

- Product surface must expose tier as a first-class choice with clear guidance, not hide it behind mode toggles.
- Cross-tier feature parity is a non-goal; features may exist in one tier and not another when the trust model requires it (e.g. mandatory HTTP_PROXY in Isolated, see ADR-0003).
