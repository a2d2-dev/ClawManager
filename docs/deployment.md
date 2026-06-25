# Deployment Guide

ClawManager is packaged as a Kubernetes-first platform. This guide is the operational entry point for deploying the control plane, locating the relevant manifests in the repository, and understanding which services are expected to come up in a working environment.

## Deployment Paths

Choose the deployment path that matches your environment:

- K3s single-node HostPath: [`deployments/k3s/single-node/clawmanager.yaml`](../deployments/k3s/single-node/clawmanager.yaml)
- K3s multi-node CSI/RWX: [`deployments/k3s/cluster/clawmanager.yaml`](../deployments/k3s/cluster/clawmanager.yaml)
- Kubernetes single-node HostPath: [`deployments/k8s/single-node/clawmanager.yaml`](../deployments/k8s/single-node/clawmanager.yaml)
- Kubernetes multi-node CSI/RWX: [`deployments/k8s/cluster/clawmanager.yaml`](../deployments/k8s/cluster/clawmanager.yaml)
- End-to-end first-use walkthrough: [User Guide](./use_guide_en.md)

The cluster profile is validated with Longhorn as the example CSI implementation. It uses `longhorn` for RWO control-plane and instance volumes, and `longhorn-rwx` for the RWX workspace volume. These names are not a project requirement; replace them with compatible StorageClasses for your storage provider.

## What Gets Deployed

- ClawManager frontend and backend
- MySQL for application state
- MinIO for object storage-backed features
- `skill-scanner` for skill analysis workflows
- Kubernetes Services used for portal, gateway, and supporting traffic paths

## Repository Entry Points

- Kubernetes single-node manifest: [`deployments/k8s/single-node/clawmanager.yaml`](../deployments/k8s/single-node/clawmanager.yaml)
- Kubernetes cluster manifest: [`deployments/k8s/cluster/clawmanager.yaml`](../deployments/k8s/cluster/clawmanager.yaml)
- K3s single-node manifest: [`deployments/k3s/single-node/clawmanager.yaml`](../deployments/k3s/single-node/clawmanager.yaml)
- K3s cluster manifest: [`deployments/k3s/cluster/clawmanager.yaml`](../deployments/k3s/cluster/clawmanager.yaml)
- Container startup script: [`deployments/container/start.sh`](../deployments/container/start.sh)
- Nginx config: [`deployments/nginx/nginx.conf`](../deployments/nginx/nginx.conf)

## Deployment Workflow

1. Choose the Kubernetes distribution: `k3s` or `k8s`.
2. Choose the storage profile: `single-node` or `cluster`.
3. Check the storage prerequisites for that profile.
4. Review the bundled manifest and adjust secrets, images, StorageClass names, and ingress exposure for your environment.
5. Deploy the platform components into the cluster.
6. Wait for the core services to become ready.
7. Validate frontend access, AI Gateway management pages, Security Center connectivity, and runtime creation flows.

Single-node example:

```bash
kubectl get nodes
kubectl label node <node> clawmanager.io/storage-node=true --overwrite
kubectl apply -f deployments/k8s/single-node/clawmanager.yaml
kubectl get pvc -n clawmanager-system
kubectl get pods -n clawmanager-system
```

Cluster example:

```bash
kubectl get storageclass longhorn longhorn-rwx
kubectl apply -f deployments/k8s/cluster/clawmanager.yaml
kubectl get pvc -n clawmanager-system
kubectl get pods -n clawmanager-system
```

## Storage Profiles

### Single-Node

The `single-node` profile is the official HostPath validation path. Label exactly one node with `clawmanager.io/storage-node=true` before installation. The manifest pins HostPath PVs through node affinity and runs `clawmanager-app` as a single replica. MySQL, Redis, MinIO, workspace, and runtime data are all backed by persistent volumes; durable data must not use `emptyDir`.

### Cluster

The `cluster` profile is the official multi-node CSI/RWX validation path. The bundled manifest uses Longhorn as an example only:

- `longhorn`: RWO MySQL, Redis, MinIO, and instance volumes
- `longhorn-rwx`: RWX workspace volume shared by ClawManager and runtime Pods

Set these environment variables in the ClawManager app deployment when replacing the storage provider:

- `CLAWMANAGER_STORAGE_PROFILE=cluster`
- `K8S_HOSTPATH_FALLBACK_ENABLED=false`
- `K8S_PVC_BIND_TIMEOUT=2m`
- `K8S_CONTROL_PLANE_STORAGE_CLASS=<rwo-storage-class>`
- `K8S_INSTANCE_STORAGE_CLASS=<rwo-storage-class>`
- `K8S_WORKSPACE_STORAGE_CLASS=<rwx-storage-class>`
- `K8S_WORKSPACE_ACCESS_MODE=ReadWriteMany`

Unsupported combinations:

- multi-node HostPath as a production or shared workspace strategy
- `local-path` or other node-local storage pretending to provide RWX across nodes
- cluster-internal Service DNS such as `workspace-store.clawmanager-system.svc.cluster.local` as an NFS server for kubelet-mounted PVs
- durable MySQL, Redis, MinIO, workspace, or object data on `emptyDir`
- cluster profile with implicit HostPath fallback

## Operational Notes

- ClawManager is designed around in-cluster services and platform-mediated access rather than direct pod exposure.
- Resource Management features depend on object storage and `skill-scanner` being available.
- Runtime workspace `.openclaw` and `.hermes` archive import/export size is controlled by `CLAWMANAGER_WORKSPACE_ARCHIVE_MAX_MIB`. The default is `500` MiB; set the env var on the ClawManager app deployment when a larger or smaller limit is needed.
- For install issues, collect `kubectl get storageclass`, `kubectl get pvc -n clawmanager-system`, `kubectl get pods -n clawmanager-system`, `kubectl get events -n clawmanager-system --sort-by=.lastTimestamp`, and `kubectl describe pvc -n clawmanager-system <pvc-name>` output before filing an issue.
- Production environments should review images, credentials, TLS, persistence, and networking policies before rollout.

## Related Guides

- [Admin and User Guide](./admin-user-guide.md)
- [Agent Control Plane Guide](./agent-control-plane.md)
- [AI Gateway Guide](./aigateway.md)
- [Security / Skill Scanner Guide](./security-skill-scanner.md)
- [Resource Management Guide](./resource-management.md)
- [Developer Guide](./developer-guide.md)
