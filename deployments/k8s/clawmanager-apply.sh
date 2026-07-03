#!/usr/bin/env bash
set -euo pipefail

# Edit these values before deploying, or override them with environment vars:
#   TENANT_SUFFIX=-hxc NODE_PORT=32443 APP_IMAGE=10.130.14.23:5000/clawmanager-hxc-app:team-update-20260701 ./clawmanager-apply.sh
#   OPENCLAW_RUNTIME_IMAGE=10.130.14.23:5000/openclaw-lite:latest HERMES_RUNTIME_IMAGE=10.130.14.23:5000/hermes-lite:latest ./clawmanager-apply.sh
#
# TENANT_SUFFIX examples:
#   empty = clawmanager-system
#   -abc  = clawmanager-abc-system
TENANT_SUFFIX="${TENANT_SUFFIX--hxc}"
NODE_PORT="${NODE_PORT:-32443}"
APP_IMAGE="${APP_IMAGE:-10.130.14.23:5000/clawmanager-hxc-app:team-update-20260701}"
OPENCLAW_RUNTIME_IMAGE="${OPENCLAW_RUNTIME_IMAGE:-ghcr.io/yuan-lab-llm/agentsruntime/openclaw-lite:latest}"
HERMES_RUNTIME_IMAGE="${HERMES_RUNTIME_IMAGE:-ghcr.io/yuan-lab-llm/agentsruntime/hermes-lite:latest}"

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MANIFEST="${1:-${ROOT}/clawmanager-tenant.yaml}"

case "${TENANT_SUFFIX}" in
  ""|-*) ;;
  *) TENANT_SUFFIX="-${TENANT_SUFFIX}" ;;
esac

if [[ ! -f "${MANIFEST}" ]]; then
  echo "ERROR: manifest not found: ${MANIFEST}" >&2
  echo >&2
  echo "Put clawmanager-tenant.yaml in the same directory as this script, or pass it explicitly:" >&2
  echo "  ./clawmanager-apply.sh /path/to/clawmanager-tenant.yaml" >&2
  exit 1
fi

SYSTEM_NAMESPACE="clawmanager${TENANT_SUFFIX}-system"
RENDERED_MANIFEST="$(mktemp)"
trap 'rm -f "${RENDERED_MANIFEST}"' EXIT

sed "s|{TENANT_SUFFIX}|${TENANT_SUFFIX}|g;s|{NODE_PORT}|${NODE_PORT}|g;s|{APP_IMAGE}|${APP_IMAGE}|g;s|{OPENCLAW_RUNTIME_IMAGE}|${OPENCLAW_RUNTIME_IMAGE}|g;s|{HERMES_RUNTIME_IMAGE}|${HERMES_RUNTIME_IMAGE}|g" \
  "${MANIFEST}" > "${RENDERED_MANIFEST}"

kubectl apply -f "${RENDERED_MANIFEST}"

# NFS volumes are mounted by kubelet on the node, not inside a Pod. Some
# clusters do not resolve *.svc.cluster.local from the node mount namespace, so
# patch NFS volumes to the workspace-store ClusterIP after the Service exists.
if kubectl -n "${SYSTEM_NAMESPACE}" get svc workspace-store >/dev/null 2>&1; then
  WORKSPACE_STORE_IP="$(kubectl -n "${SYSTEM_NAMESPACE}" get svc workspace-store -o jsonpath='{.spec.clusterIP}')"
  if [[ -n "${WORKSPACE_STORE_IP}" && "${WORKSPACE_STORE_IP}" != "None" ]]; then
    echo "Patching workspace NFS server to workspace-store ClusterIP: ${WORKSPACE_STORE_IP}"

    kubectl -n "${SYSTEM_NAMESPACE}" set env deployment/clawmanager-app \
      "RUNTIME_WORKSPACE_NFS_SERVER=${WORKSPACE_STORE_IP}" \
      "RUNTIME_WORKSPACE_NFS_PATH=/"

    patch_workspace_volume() {
      local deployment="$1"
      local volume_index=""
      local current_index
      local volume_name

      current_index=0
      while IFS= read -r volume_name; do
        if [[ "${volume_name}" == "workspaces" ]]; then
          volume_index="${current_index}"
          break
        fi
        current_index=$((current_index + 1))
      done < <(kubectl -n "${SYSTEM_NAMESPACE}" get deployment "${deployment}" -o jsonpath='{range .spec.template.spec.volumes[*]}{.name}{"\n"}{end}')

      if [[ -z "${volume_index}" ]]; then
        echo "WARNING: deployment/${deployment} has no workspaces volume; skipping NFS patch" >&2
        return 0
      fi

      kubectl -n "${SYSTEM_NAMESPACE}" patch deployment "${deployment}" --type=json -p \
        "[{\"op\":\"replace\",\"path\":\"/spec/template/spec/volumes/${volume_index}/nfs/server\",\"value\":\"${WORKSPACE_STORE_IP}\"},{\"op\":\"replace\",\"path\":\"/spec/template/spec/volumes/${volume_index}/nfs/path\",\"value\":\"/\"}]"
    }

    patch_workspace_volume clawmanager-app

    for deployment in openclaw-runtime hermes-runtime; do
      if kubectl -n "${SYSTEM_NAMESPACE}" get deployment "${deployment}" >/dev/null 2>&1; then
        patch_workspace_volume "${deployment}"
      fi
    done
  fi
fi
