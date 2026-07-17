# AI Gateway is a single centralized layer, not edge/central split

ClawManager runs one AI Gateway per deployed cluster (reached by runtime gateways as an in-cluster Kubernetes Service), and does NOT split it into an edge-enforcement layer plus a central-policy/audit layer. Since the standard ClawManager deployment shape is "one independent cluster per customer edge site," the in-cluster gateway already sits at the enforcement point; a second, higher layer would only exist to unify policy across sites we don't have.

## Considered Options

- **Cloudflare-style dual-layer** (edge enforcer at every POP + central policy plane) — rejected because it presumes a global-POP-plus-central-brain topology. Our edges are independent per-customer clusters, so the "edge enforcer" and "the whole gateway" are the same thing; a second layer would introduce cross-cluster policy-sync complexity with no offsetting benefit.

## Consequences

- If a future deployment does put multiple edge clusters under one customer with a requirement for unified policy and audit, this ADR is the point to revisit — the split becomes worth its cost only when cross-cluster coherence is a real need.
- Per-site policy divergence is expected; there is no central source of truth for gateway rules across sites.
