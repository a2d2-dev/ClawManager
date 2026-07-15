# NodeGroup is owned by the enterprise layer; ClawManager only passes through placement

Issue #1 assumed ClawManager would integrate a nodegroup model (tenant admission, capacity profiles, runtime-class declarations). We decided ClawManager, as an open-source project, does not own a nodegroup concept at all. That concept lives in the enterprise layer (edge-apiserver / Rise Global `scope/v1alpha1 NodeGroup` — a named node selector CRD plus IAM scoping).

ClawManager's runtime backend spec instead exposes generic placement passthrough fields — `nodeSelector` (matchLabels + matchExpressions, structurally aligned with edge-apiserver's `NodeSelector`), `runtimeClassName`, and `tolerations` — so an enterprise NodeGroup resolves to labels and flows in without ClawManager knowing about "groups".

## Consequences

- The "Nodegroup / Multi-Tenant Integration" section of issue #1 is out of scope here; it becomes an enterprise-layer integration point.
- ClawManager admin UI shows raw placement fields, not group names.
