# Agent Sandbox Substrate Gap List

Date: 2026-07-17 UTC

Issue: `a2d2-dev/ClawManager#3`

Workload namespace: `clawmanager-sandbox-spike`

Controller namespace: `agent-sandbox-system`

## Install Method And Version

Installed and re-applied the upstream release manifest pinned to `v0.5.1`:

```bash
kubectl apply -f https://github.com/kubernetes-sigs/agent-sandbox/releases/download/v0.5.1/manifest.yaml
```

Observed output:

```text
namespace/agent-sandbox-system unchanged
serviceaccount/agent-sandbox-controller unchanged
clusterrolebinding.rbac.authorization.k8s.io/agent-sandbox-controller unchanged
role.rbac.authorization.k8s.io/agent-sandbox-controller unchanged
rolebinding.rbac.authorization.k8s.io/agent-sandbox-controller unchanged
service/agent-sandbox-controller unchanged
deployment.apps/agent-sandbox-controller unchanged
service/agent-sandbox-webhook-service unchanged
customresourcedefinition.apiextensions.k8s.io/sandboxes.agents.x-k8s.io unchanged
clusterrole.rbac.authorization.k8s.io/agent-sandbox-controller unchanged
```

Release and install evidence:

```bash
gh release view v0.5.1 --repo kubernetes-sigs/agent-sandbox --json tagName,publishedAt,targetCommitish,url,isDraft,isPrerelease
kubectl -n agent-sandbox-system get deploy agent-sandbox-controller -o jsonpath='image={.spec.template.spec.containers[0].image}{" replicas="}{.status.readyReplicas}{"/"}{.status.replicas}{" namespace="}{.metadata.namespace}{"\n"}'
kubectl get crd sandboxes.agents.x-k8s.io -o jsonpath='versions={range .spec.versions[*]}{.name}:served={.served},storage={.storage};{end}{"\n"}'
```

```text
{"isDraft":false,"isPrerelease":false,"publishedAt":"2026-07-09T23:34:40Z","tagName":"v0.5.1","targetCommitish":"main","url":"https://github.com/kubernetes-sigs/agent-sandbox/releases/tag/v0.5.1"}
image=registry.k8s.io/agent-sandbox/agent-sandbox-controller:v0.5.1 replicas=1/1 namespace=agent-sandbox-system
versions=v1beta1:served=true,storage=true;v1alpha1:served=true,storage=false;
```

## Manifests And Image Choice

Manifests: `docs/spike/agent-sandbox/openclaw-hermes-sandboxes.yaml`

Existing runtime images found in the cluster:

```bash
kubectl get pods -A -o jsonpath='{range .items[*]}{.metadata.namespace}{"\t"}{.metadata.name}{"\t"}{range .spec.containers[*]}{.name}{":"}{.image}{";"}{end}{"\n"}{end}' | rg -i 'openclaw|hermes|runtime|gateway'
```

```text
clawmanager-system	hermes-runtime-7758fcbbf8-t75gv	runtime:swr.cn-east-3.myhuaweicloud.com/risecloud/clawmanager-hermes-lite:latest;
clawmanager-system	openclaw-runtime-785d46bd76-n7qq7	runtime:swr.cn-east-3.myhuaweicloud.com/risecloud/clawmanager-openclaw-lite:latest;
```

Chosen images:

- OpenClaw: `swr.cn-east-3.myhuaweicloud.com/risecloud/clawmanager-openclaw-lite:latest`
- Hermes: `swr.cn-east-3.myhuaweicloud.com/risecloud/clawmanager-hermes-lite:latest`

Assumption: the `latest` tags are used because those exact images are already deployed in `clawmanager-system`; repo docs describe the Lite runtime/gateway shape, but did not provide immutable digests for this spike.

Gateway-only process evidence:

```bash
kubectl -n clawmanager-sandbox-spike exec openclaw-gateway -- ps -o pid,ppid,comm,args
kubectl -n clawmanager-sandbox-spike exec hermes-gateway -- ps -o pid,ppid,comm,args
```

```text
PID  PPID COMMAND         COMMAND
  1     0 openclaw        openclaw

PID  PPID COMMAND         COMMAND
  1     0 bash            bash /usr/local/bin/start-hermes-dashboard-gateway
 25     1 hermes          /usr/local/lib/hermes-agent/venv/bin/python3 /usr/local/lib/hermes-agent/venv/bin/hermes dashboard --host 0.0.0.0 --port 19090 --no-open --insecure --skip-build
```

## Current Sandbox Status

Final state after validation cleanup:

```bash
kubectl -n clawmanager-sandbox-spike get sandbox,pod,pvc,svc -o wide
```

```text
NAME                                       READY   REASON              AGE
sandbox.agents.x-k8s.io/hermes-gateway     True    DependenciesReady   35h
sandbox.agents.x-k8s.io/openclaw-gateway   True    DependenciesReady   35h

NAME                   READY   STATUS    RESTARTS   AGE     IP               NODE
pod/hermes-gateway     1/1     Running   0          3m22s   10.233.120.230   ecs-liufeng-001
pod/openclaw-gateway   1/1     Running   0          3m22s   10.233.120.227   ecs-liufeng-001

NAME                                               STATUS   VOLUME                                     CAPACITY   ACCESS MODES   STORAGECLASS
persistentvolumeclaim/config-hermes-gateway        Bound    pvc-c1bf6acb-a3c2-4a1c-8b50-bb04308c0cb6   1Gi        RWO            local
persistentvolumeclaim/config-openclaw-gateway      Bound    pvc-b36f154b-564a-46a1-be8f-2bec8e3de4d5   1Gi        RWO            local
persistentvolumeclaim/workspace-hermes-gateway     Bound    pvc-0dfea079-8084-4ea7-830e-8e39810db296   2Gi        RWO            local
persistentvolumeclaim/workspace-openclaw-gateway   Bound    pvc-57948ed4-10cf-4fbf-9e8a-5b71b13c8d96   2Gi        RWO            local

NAME                       TYPE        CLUSTER-IP   EXTERNAL-IP   PORT(S)
service/hermes-gateway     ClusterIP   None         <none>        <none>
service/openclaw-gateway   ClusterIP   None         <none>        <none>
```

In-cluster reachability after resume:

```bash
kubectl -n clawmanager-sandbox-spike run reachability-openclaw-postresume --image=curlimages/curl:8.10.1 --restart=Never --rm -i --overrides='{"spec":{"nodeSelector":{"kubernetes.io/hostname":"ecs-liufeng-001"},"containers":[{"name":"reachability-openclaw-postresume","image":"curlimages/curl:8.10.1","command":["sh","-lc","set -eu; getent hosts openclaw-gateway; curl -sS --max-time 10 http://openclaw-gateway:19090/health"]}]}}'
kubectl -n clawmanager-sandbox-spike run reachability-hermes-postresume --image=curlimages/curl:8.10.1 --restart=Never --rm -i --overrides='{"spec":{"nodeSelector":{"kubernetes.io/hostname":"ecs-liufeng-001"},"containers":[{"name":"reachability-hermes-postresume","image":"curlimages/curl:8.10.1","command":["sh","-lc","set -eu; getent hosts hermes-gateway; curl -sS -i --max-time 10 http://hermes-gateway:19090/ | sed -n '1,8p'"]}]}}'
```

```text
10.233.120.227    openclaw-gateway.clawmanager-sandbox-spike.svc.cluster.local  openclaw-gateway.clawmanager-sandbox-spike.svc.cluster.local openclaw-gateway
{"ok":true,"status":"live"}pod "reachability-openclaw-postresume" deleted

10.233.120.230    hermes-gateway.clawmanager-sandbox-spike.svc.cluster.local  hermes-gateway.clawmanager-sandbox-spike.svc.cluster.local hermes-gateway
HTTP/1.1 200 OK
date: Fri, 17 Jul 2026 03:11:35 GMT
server: uvicorn
cache-control: no-store, no-cache, must-revalidate
```

## Stop And Resume Result

Fresh markers written before stop:

```text
issue-3-20260717T0311Z-openclaw
issue-3-20260717T0311Z-hermes
```

Stop operation:

```bash
kubectl -n clawmanager-sandbox-spike patch sandbox openclaw-gateway --type=merge -p '{"spec":{"operatingMode":"Suspended"}}'
kubectl -n clawmanager-sandbox-spike patch sandbox hermes-gateway --type=merge -p '{"spec":{"operatingMode":"Suspended"}}'
kubectl -n clawmanager-sandbox-spike wait --for=delete pod/openclaw-gateway pod/hermes-gateway --timeout=180s
kubectl -n clawmanager-sandbox-spike get sandbox,pod,pvc,svc -o wide
```

```text
sandbox.agents.x-k8s.io/openclaw-gateway patched
sandbox.agents.x-k8s.io/hermes-gateway patched
pod/openclaw-gateway condition met

NAME                                       READY   REASON             AGE
sandbox.agents.x-k8s.io/hermes-gateway     False   SandboxSuspended   35h
sandbox.agents.x-k8s.io/openclaw-gateway   False   SandboxSuspended   35h

NAME                                               STATUS   VOLUME                                     CAPACITY   ACCESS MODES   STORAGECLASS
persistentvolumeclaim/config-hermes-gateway        Bound    pvc-c1bf6acb-a3c2-4a1c-8b50-bb04308c0cb6   1Gi        RWO            local
persistentvolumeclaim/config-openclaw-gateway      Bound    pvc-b36f154b-564a-46a1-be8f-2bec8e3de4d5   1Gi        RWO            local
persistentvolumeclaim/workspace-hermes-gateway     Bound    pvc-0dfea079-8084-4ea7-830e-8e39810db296   2Gi        RWO            local
persistentvolumeclaim/workspace-openclaw-gateway   Bound    pvc-57948ed4-10cf-4fbf-9e8a-5b71b13c8d96   2Gi        RWO            local
```

Resume operation:

```bash
kubectl -n clawmanager-sandbox-spike patch sandbox openclaw-gateway --type=merge -p '{"spec":{"operatingMode":"Running"}}'
kubectl -n clawmanager-sandbox-spike patch sandbox hermes-gateway --type=merge -p '{"spec":{"operatingMode":"Running"}}'
kubectl -n clawmanager-sandbox-spike wait --for=condition=Ready pod/openclaw-gateway pod/hermes-gateway --timeout=180s
kubectl -n clawmanager-sandbox-spike exec openclaw-gateway -- cat /workspaces/agent-sandbox-marker.txt
kubectl -n clawmanager-sandbox-spike exec hermes-gateway -- cat /workspaces/agent-sandbox-marker.txt
```

```text
sandbox.agents.x-k8s.io/openclaw-gateway patched
sandbox.agents.x-k8s.io/hermes-gateway patched
pod/openclaw-gateway condition met
pod/hermes-gateway condition met
issue-3-20260717T0311Z-openclaw
issue-3-20260717T0311Z-hermes
```

Conclusion: product `Stop` should map to `Sandbox.spec.operatingMode=Suspended`; product `Start` should map to `Running`. In this cluster, `Suspended` deleted the Pods, preserved the PVCs, and marker files under `/workspaces` survived Resume unchanged.

## Hypotheses Tested

1. `status.conditions` surface: confirmed, but not cleanly exclusive.

```bash
kubectl explain sandbox.status --api-version=agents.x-k8s.io/v1beta1
kubectl -n clawmanager-sandbox-spike get sandbox openclaw-gateway -o yaml | sed -n '/^status:/,$p'
```

```text
FIELDS:
  conditions	<[]Object>
  nodeName	<string>
  podIPs	<[]string>
  selector	<string>
  service	<string>
  serviceFQDN	<string>

status:
  conditions:
  - message: Pod is Ready; Service Exists
    observedGeneration: 4
    reason: DependenciesReady
    status: "True"
    type: Ready
  - message: Pod has been terminated. Sandbox is not operational.
    observedGeneration: 3
    reason: PodTerminated
    status: "True"
    type: Suspended
```

2. `Finished(PodFailed)` no-auto-recreate: confirmed with temporary `failure-probe`.

```bash
kubectl -n clawmanager-sandbox-spike get sandbox failure-probe -o yaml | sed -n '/^status:/,$p'
kubectl -n clawmanager-sandbox-spike get pod failure-probe -o jsonpath='name={.metadata.name} uid={.metadata.uid} phase={.status.phase} restartPolicy={.spec.restartPolicy} restartCount={.status.containerStatuses[0].restartCount} exitCode={.status.containerStatuses[0].state.terminated.exitCode} node={.spec.nodeName}{"\n"}'
sleep 45
kubectl -n clawmanager-sandbox-spike get pod failure-probe -o jsonpath='name={.metadata.name} uid={.metadata.uid} phase={.status.phase} restartCount={.status.containerStatuses[0].restartCount} age={.metadata.creationTimestamp}{"\n"}'
```

```text
status:
  conditions:
  - message: Pod failed
    observedGeneration: 1
    reason: PodFailed
    status: "False"
    type: Ready
  - message: Pod failed
    observedGeneration: 1
    reason: PodFailed
    status: "True"
    type: Finished
name=failure-probe uid=02e563a6-60d4-4a35-8d54-7a44569d15c1 phase=Failed restartPolicy=Never restartCount=0 exitCode=42 node=ecs-liufeng-001
name=failure-probe uid=02e563a6-60d4-4a35-8d54-7a44569d15c1 phase=Failed restartCount=0 age=2026-07-17T03:12:18Z
```

3. Sandbox CR has no `networkPolicy` field: confirmed.

```bash
kubectl explain sandbox.spec.networkPolicy --api-version=agents.x-k8s.io/v1beta1
kubectl api-resources --api-group=agents.x-k8s.io -o wide
```

```text
error: field "networkPolicy" does not exist

NAME        SHORTNAMES   APIVERSION                NAMESPACED   KIND      VERBS
sandboxes   sandbox      agents.x-k8s.io/v1beta1   true         Sandbox   delete,deletecollection,get,list,patch,create,update,watch
```

4. `nodeSelector` and `tolerations` pass through: confirmed. `runtimeClassName` is schema-supported but not live-tested because this cluster has no RuntimeClass objects.

```bash
kubectl explain sandbox.spec.podTemplate.spec --api-version=agents.x-k8s.io/v1beta1 | sed -n '1,220p'
kubectl get runtimeclass
kubectl -n clawmanager-sandbox-spike get pod passthrough-probe -o jsonpath='nodeSelector={.spec.nodeSelector}{"\n"}tolerations={.spec.tolerations}{"\n"}runtimeClassName={.spec.runtimeClassName}{"\n"}automount={.spec.automountServiceAccountToken}{"\n"}'
```

```text
FIELDS:
  nodeSelector	<map[string]string>
  runtimeClassName	<string>
  tolerations	<[]Object>

No resources found

nodeSelector={"kubernetes.io/hostname":"ecs-liufeng-001"}
tolerations=[{"effect":"NoSchedule","key":"clawmanager.io/passthrough-probe","operator":"Exists"},{"effect":"NoExecute","key":"node.kubernetes.io/not-ready","operator":"Exists","tolerationSeconds":300},{"effect":"NoExecute","key":"node.kubernetes.io/unreachable","operator":"Exists","tolerationSeconds":300}]
runtimeClassName=
automount=false
```

5. No upstream identity/token surface: confirmed.

```bash
kubectl explain sandbox.spec --api-version=agents.x-k8s.io/v1beta1
kubectl explain sandbox.spec.podTemplate.spec.serviceAccountName --api-version=agents.x-k8s.io/v1beta1
kubectl explain sandbox.spec.podTemplate.spec.containers.env --api-version=agents.x-k8s.io/v1beta1 | sed -n '1,80p'
```

```text
FIELDS:
  operatingMode	<string>
  podTemplate	<Object> -required-
  service	<boolean>
  shutdownPolicy	<string>
  shutdownTime	<string>
  volumeClaimTemplates	<[]Object>

FIELD: serviceAccountName <string>

FIELD: env <[]Object>
FIELDS:
  name	<string> -required-
  value	<string>
  valueFrom	<Object>
```

## Gap List

- `solved in ClawManager`: Product stopped/start semantics are a patch to `spec.operatingMode`, not a named API action. Justification: ClawManager can map Stop to `Suspended` and Start to `Running`. Evidence: `kubectl -n clawmanager-sandbox-spike patch sandbox ... '{"spec":{"operatingMode":"Suspended"}}'` deleted both Pods, left workspace PVCs `Bound`, then `Running` recreated Pods and `cat /workspaces/agent-sandbox-marker.txt` returned `issue-3-20260717T0311Z-openclaw` and `issue-3-20260717T0311Z-hermes`.

- `upstream PR to agent-sandbox`: `Finished(PodFailed)` is terminal and did not auto-recreate a replacement Pod while the Sandbox was otherwise in running mode. Isolated mode needs a clean self-healing or restart contract. Evidence: after a 45 second observation window, `kubectl -n clawmanager-sandbox-spike get pod failure-probe ...` returned the same UID `02e563a6-60d4-4a35-8d54-7a44569d15c1`, `phase=Failed`, and `restartCount=0`.

- `upstream PR to agent-sandbox`: `spec.service: true` creates a headless Service without ports even when the container declares `containerPort: 19090`; consumers must know the gateway port out of band. Evidence: `kubectl -n clawmanager-sandbox-spike get sandbox,pod,pvc,svc -o wide` showed `service/openclaw-gateway` and `service/hermes-gateway` as `ClusterIP None` with `PORT(S) <none>`, while reachability required explicit `http://openclaw-gateway:19090/health` and `http://hermes-gateway:19090/`.

- `upstream PR to agent-sandbox`: Status conditions are not a clean current-state summary; a resumed running Sandbox can still show stale `Suspended=True/PodTerminated` with an older `observedGeneration`. Evidence: `kubectl -n clawmanager-sandbox-spike get sandbox openclaw-gateway -o yaml` showed `Ready=True/DependenciesReady` at `observedGeneration: 4` and also `Suspended=True/PodTerminated` at `observedGeneration: 3`.

- `solved in ClawManager`: Workload health is not the same as Sandbox dependency readiness. Justification: ClawManager owns the gateway commands and can inject `readinessProbe` or call each gateway health endpoint. Evidence: Sandbox status only reported `message: Pod is Ready; Service Exists`, while real application verification required `curl http://openclaw-gateway:19090/health` returning `{"ok":true,"status":"live"}` and `curl http://hermes-gateway:19090/` returning `HTTP/1.1 200 OK`.

- `solved in ClawManager`: Network isolation is not modeled on the Sandbox CR. Justification: ClawManager should render separate Kubernetes `NetworkPolicy` resources in the workload namespace when Isolated mode needs them. Evidence: `kubectl explain sandbox.spec.networkPolicy --api-version=agents.x-k8s.io/v1beta1` returned `error: field "networkPolicy" does not exist`, and `kubectl api-resources --api-group=agents.x-k8s.io -o wide` exposed only `sandboxes`.

- `solved in ClawManager`: Identity, model tokens, and gateway auth are not first-class agent-sandbox fields. Justification: ClawManager must inject them through `podTemplate` env vars, Secrets, service accounts, or volumes and own rotation policy. Evidence: `kubectl explain sandbox.spec` listed only `operatingMode`, `podTemplate`, `service`, `shutdownPolicy`, `shutdownTime`, and `volumeClaimTemplates`; `kubectl explain sandbox.spec.podTemplate.spec.containers.env` exposed normal Kubernetes env/valueFrom fields.

- `solved in ClawManager`: Placement and runtime policy are Kubernetes PodSpec policy, not product concepts in agent-sandbox. Justification: ClawManager should validate and render node selectors, tolerations, affinity, and runtime class choices from its own instance policy. Evidence: the passthrough probe generated Pod showed `nodeSelector={"kubernetes.io/hostname":"ecs-liufeng-001"}` and a custom `clawmanager.io/passthrough-probe` toleration copied into `.spec.tolerations`; runtimeClassName is present in schema but not live-tested because `kubectl get runtimeclass` returned `No resources found`.

- `solved in ClawManager`: Persistent workspace policy is product policy. Justification: agent-sandbox can create PVCs from `volumeClaimTemplates`, but ClawManager must choose size, StorageClass, mount path, retention, and deletion behavior. Evidence: the Sandbox manifests created `workspace-*` PVCs with `2Gi` and `config-*` PVCs with `1Gi`, all `Bound` on StorageClass `local`; marker files under `/workspaces` survived `Suspended` and `Running`.

- `solved in ClawManager`: Product instance correlation requires product metadata. Justification: agent-sandbox status gives selectors, service, serviceFQDN, podIPs, and nodeName; ClawManager must persist the Sandbox namespace/name against the instance record and query child resources by selector when it needs Pod/PVC details. Evidence: `kubectl explain sandbox.status` listed `conditions`, `nodeName`, `podIPs`, `selector`, `service`, and `serviceFQDN`, but no product instance id, PVC names, container restart count, or explicit pod name field.

- `solved in ClawManager`: Gateway-only runtime behavior is not guaranteed by the existing images' defaults. Justification: ClawManager should use dedicated gateway-only image entrypoints or render explicit commands, as the spike did. Evidence: the manifest overrides OpenClaw with `/usr/local/bin/openclaw gateway run ... --port 19090` and Hermes with `/usr/local/bin/start-hermes-dashboard-gateway`; process output showed only `openclaw` PID 1 for OpenClaw and `hermes dashboard --host 0.0.0.0 --port 19090` for Hermes.

- `solved in ClawManager`: Private-by-default service exposure is compatible, but external/user access remains a ClawManager routing concern. Justification: the observed Services are cluster-internal headless Services; ClawManager should continue routing through its own gateway/proxy rather than exposing Sandbox Services directly. Evidence: `kubectl -n clawmanager-sandbox-spike get svc` showed `TYPE ClusterIP`, `CLUSTER-IP None`, and `EXTERNAL-IP <none>` for both Services, while in-cluster temporary curl Pods could resolve and reach the gateway DNS names.

## Cleanup Notes

Temporary validation Sandboxes `failure-probe` and `passthrough-probe` were deleted after evidence capture. Final namespace state contains only the required OpenClaw and Hermes gateway Sandboxes plus their Pods, Services, and PVCs.

Full cleanup commands for later, not run as part of this issue completion:

```bash
kubectl delete -f docs/spike/agent-sandbox/openclaw-hermes-sandboxes.yaml --ignore-not-found
kubectl delete namespace clawmanager-sandbox-spike --ignore-not-found
kubectl delete -f https://github.com/kubernetes-sigs/agent-sandbox/releases/download/v0.5.1/manifest.yaml --ignore-not-found
```
