# ClawManager

ClawManager is a Kubernetes-native control plane for AI Agent instance management. It provisions per-user Agent runtimes (小龙虾 / Hermes), governs their access to models and external services through a centralized AI Gateway, and manages reusable resources (skills, channels, session templates) across multiple runtime types.

## Language

### Runtime and instance model

**小龙虾 (Xiaolongxia)**:
Nickname for a per-user OpenClaw Agent instance. In office scenarios, one user has exactly one 小龙虾; it is long-lived and sticky.
_Avoid_: "Agent instance", "OpenClaw pod", "user's agent"

**多开 (Duokai)**:
Platform-level concurrency: N users × 1 instance each running simultaneously on the platform. Explicitly NOT "one user opens multiple instances".
_Avoid_: "multi-instance per user", "instance replication"

**Runtime Pod**:
A Kubernetes Pod that hosts exactly one runtime agent (control process) plus one or more gateway subprocesses. In Lite mode, one Runtime Pod hosts many users' gateways (up to `capacity`, e.g. 100); in Pro mode, one Runtime Pod ≈ one user.
_Avoid_: "agent pod", "runtime container"

**Runtime Agent**:
The single control-plane process inside every Runtime Pod. Owns port allocation, workspace creation, gateway subprocess lifecycle, health checks, and reports to Agent Control Plane. Not the business Agent itself.
_Avoid_: "agent daemon", "control agent"

**Gateway (subprocess)**:
The per-instance business process launched by the Runtime Agent. One gateway = one user instance = one workspace. Runs under a per-instance UID/GID with cgroup limits.
_Avoid_: "instance process", "user process", "runtime process". Note: do not confuse with "AI Gateway" — different concept, different layer.

**instance_mode: lite / isolated / pro**:
Three parallel tiers, picked at instance creation. Enum lives in the backend as `instance_mode`; kept orthogonal to `runtime_type` (which describes the workload — `gateway` / `desktop` / `shell`), so code never infers one from the other.

- **lite** = container + web service, packed multi-tenant into a shared Runtime Pod (up to `capacity` per Pod). Cheapest. Correctness-level cross-user isolation only (UID/GID + workspace path + cgroup).
- **isolated** = one headless Kubernetes isolation unit per instance, backed by an `agent-sandbox` `Sandbox` CRD object. No desktop stack. Persistent workspace. For scenarios where the Agent is expected to be hijacked via prompt injection; contains blast radius to a single Pod. Runtime class (gVisor/Kata) can be passed through when the cluster provides one.
- **pro** = container + Webtop (KasmVNC) desktop, one dedicated Runtime Pod per instance. Full graphical environment.

Never migrate an instance across tiers to satisfy a stronger requirement — pick the right tier at creation. See ADR-0001.
_Avoid_: "Hijack-safe" (early placeholder; superseded), "shared vs dedicated", "secure runtime" (lite/pro aren't insecure — different threat model)

**RuntimeBackend**:
The Go interface that lifecycle operations for all instance modes route through. One implementation per mode: `liteBackend`, `proDesktopBackend`, `sandboxBackend`. Introduced to keep mode-specific logic out of shared code paths; call sites pass the requested mode explicitly rather than infer it from `runtime_type`.
_Avoid_: "instance backend", "mode backend"

**agent-sandbox**:
The kubernetes-sigs open-source project (`kubernetes-sigs/agent-sandbox`) providing the `Sandbox` CRD used by `sandboxBackend`. Manages one isolated, stateful, singleton Pod per Sandbox object with stable identity, persistent storage, and lifecycle management. Isolated mode requires the CRDs to be installed on the cluster — no native-Pod fallback is currently proposed.
_Avoid_: "Agent Runtime" (ambiguous — could be the OSS project OR our runtime agent inside a Pod; use the exact name "agent-sandbox" for the project), "sandbox operator"

**runtime_type vs instance_mode**:
Two orthogonal axes in the backend domain model. `runtime_type` describes WHAT is being run (`gateway` / `desktop` / `shell`). `instance_mode` describes HOW isolated/expensive the instance is (`lite` / `isolated` / `pro`). Code MUST NOT infer one from the other — callers pass both explicitly. Isolated mode currently only supports headless gateway runtimes.
_Avoid_: conflating "mode" and "type"

**Workspace**:
Per-instance persistent directory at `/workspaces/{runtime}/user-{user_id}/instance-{instance_id}`. Owned by the instance's UID/GID; the boundary against cross-user filesystem access.
_Avoid_: "user home", "user dir", "instance volume"

### Control plane and governance

**Agent Control Plane**:
The ClawManager-side orchestration component that runtime agents register with. Issues commands (start/stop/restart/apply_config/install_skill), tracks desired state, and consumes runtime heartbeats. Does NOT execute business Agent logic itself.
_Avoid_: "control plane" (ambiguous), "orchestrator"

**AI Gateway**:
The centralized in-cluster governance plane for all model calls from all instances. Enforces model whitelisting, risk-rule scanning, secure-model routing, and per-model / per-user / per-instance cost accounting. Every LLM call from a gateway subprocess flows through it via the injected `CLAWMANAGER_LLM_BASE_URL`.
_Avoid_: "LLM proxy", "model gateway", "OpenAI proxy". Not to be confused with the per-instance "gateway subprocess".

**Skill**:
A packaged, distributable capability unit for an Agent runtime. Two shapes: (1) configuration skill (JSON injected at instance creation), (2) skill package (zip installed via `install_skill` command). Identified by `content_md5` fingerprint.
_Avoid_: "plugin", "tool", "extension"

**Channel**:
A messaging integration configuration for the Agent runtime, e.g. Feishu, Telegram, Slack, DingTalk. Injected as JSON at instance creation and materialized by the runtime agent into the runtime's native config.
_Avoid_: "notifier", "connector", "integration"

**Bootstrap manifest**:
The JSON descriptor listing which channels, skills, session templates, agents, and scheduled tasks were injected into a specific instance at creation time. Used as an idempotency key by the runtime agent — same hash means no reapply.
_Avoid_: "config manifest", "provisioning manifest"

### Security and threat model

**Hijack-level isolation**:
A threat model that assumes the Agent inside a 小龙虾 will be hijacked via prompt injection from external content the user asks it to open (web page, doc, email). Isolation must contain the blast radius to that single instance. Only applies to the **isolated** tier — lite and pro explicitly do NOT carry this guarantee. User themselves is trusted (intranet deployment), so kernel escape is out of scope.
_Avoid_: "sandbox" (agent-sandbox is a specific CRD project, don't overload the word), "secure isolation", "adversarial isolation"

**Same-node multi-tenancy**:
The deployment reality: one edge node may host multiple users' 小龙虾. In Lite mode this compresses further to *same-Pod multi-tenancy* — many users' gateway subprocesses in a single Runtime Pod sharing the Pod's network namespace and filesystem view.
_Avoid_: "shared node", "colocated tenancy"

**Edge computing mode**:
The default deployment shape: ClawManager control plane and Runtime Pods both run on edge nodes/clusters near the user, not centralized in a public cloud. Implies latency, offline availability, and per-site policy considerations.
_Avoid_: "on-prem", "distributed deployment"

**Instance token**:
A per-instance credential (`CLAWMANAGER_LLM_API_KEY = "instance-token"`, `CLAWMANAGER_INSTANCE_TOKEN`) that scopes what a gateway subprocess can do when calling AI Gateway. Distinct from the runtime agent's own `RUNTIME_AGENT_CONTROL_TOKEN` and `RUNTIME_AGENT_REPORT_TOKEN`.
_Avoid_: "API key", "user token"

**Trusted proxy**:
The HTTP-layer trust model between ClawManager backend and runtime gateways. The runtime gateway configures ClawManager's internal CIDR (`CLAWMANAGER_TRUSTED_PROXY_CIDRS`) as trusted and skips its own device-auth handshake when requests arrive via that path. Enables single-sign-through without users pasting gateway tokens.
_Avoid_: "reverse proxy trust", "proxy allowlist"
