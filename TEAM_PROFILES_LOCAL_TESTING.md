# Team Profiles 本地测试与启动说明

本文记录本地 `kind + localhost:30443` 测试 Team 页面、角色模板、团队群聊、Execution Kanban、共享文件浏览器等功能时的正确方式。

## 当前结论

本地测试使用两个 YAML：

- `deployments/k8s/clawmanager.yaml`：完整基础部署，来自 upstream。
- `deployments/k8s/clawmanager-team-profiles-test.yaml`：只用于把 `clawmanager-app` 镜像切换成本地构建的 `clawmanager:team-profiles-test`，并补齐本地测试需要的 app 环境变量。

合并 upstream 后，Team 默认支持新的 `Lite` runtime pool 逻辑。服务器环境应该保留这套逻辑，不要为了本地 kind 绕开它。

但 Docker Desktop / kind 上，`workspace-store` 使用的 NFS export 可能无法正常工作，常见日志是：

```text
exportfs: /exports/workspaces does not support NFS export
Export validation failed, exiting...
```

这会导致 `clawmanager-app` 如果直接挂载 `/workspaces` NFS，就卡在 `ContainerCreating`，`rollout status` 显示：

```text
Waiting for deployment "clawmanager-app" rollout to finish: 1 old replicas are pending termination...
```

这不是业务代码问题，也不是旧 Pod 删不掉，而是新 Pod 因 NFS 挂载失败无法 Ready。

因此本地测试 overlay 不再让 `clawmanager-app` 自身挂载 `/workspaces` NFS。服务器部署文件和 upstream 的 Lite runtime 逻辑不变。

## 本地推荐测试范围

本地 kind 推荐验证：

- ClawManager 前端页面和 Team 详情页布局。
- Team 角色模板、模板包、成员配置。
- Pro/桌面实例模式下的 Team 群聊链路。
- Execution Kanban 展示、问题锚点、文件浏览器、文件预览等页面功能。

本地 kind 不推荐验证：

- Lite runtime pool 的完整调度链路。
- `workspace-store` NFS 共享目录挂载。

Lite runtime pool 建议在服务器或支持 NFS 的 Kubernetes 集群验证。

## 正确启动流程

必须在项目根目录执行，因为 Dockerfile 在根目录：

```powershell
cd D:\test\ClawManager-2
```

构建本地 ClawManager 镜像：

```powershell
docker build -t clawmanager:team-profiles-test .
kind load docker-image clawmanager:team-profiles-test --name my-cluster
kubectl apply -f deployments/k8s/clawmanager.yaml
kubectl apply -f deployments/k8s/clawmanager-team-profiles-test.yaml
kubectl -n clawmanager-system rollout status deployment/clawmanager-app
kubectl port-forward -n clawmanager-system svc/clawmanager-frontend 30443:443
```

把镜像加载进 kind 集群：

```powershell
kind load docker-image clawmanager:team-profiles-test --name my-cluster
```

先 apply 完整基础部署。合并 upstream 后，这一步会创建 runtime pool 相关资源：

```powershell
kubectl apply -f deployments/k8s/clawmanager.yaml
```

再 apply 本地测试覆盖文件：

```powershell
kubectl apply -f deployments/k8s/clawmanager-team-profiles-test.yaml
```

等待 app 启动完成：

```powershell
kubectl -n clawmanager-system rollout status deployment/clawmanager-app
```

启动端口转发：

```powershell
kubectl port-forward -n clawmanager-system svc/clawmanager-frontend 30443:443
```

浏览器打开：

```text
https://localhost:30443
```

## 改完前端或后端后怎么重启

前端和后端都会被打进 `clawmanager-app` 镜像，所以改完代码后使用同一套流程：

```powershell
cd D:\test\ClawManager-2

docker build -t clawmanager:team-profiles-test .
kind load docker-image clawmanager:team-profiles-test --name my-cluster

kubectl apply -f deployments/k8s/clawmanager-team-profiles-test.yaml
kubectl -n clawmanager-system rollout restart deployment/clawmanager-app
kubectl -n clawmanager-system rollout status deployment/clawmanager-app
```

通常不需要重新 apply `clawmanager.yaml`。只有以下情况才需要重新 apply 基础部署：

- upstream 更新了 Kubernetes 资源。
- runtime pool 相关 YAML 变了。
- `workspace-store`、`openclaw-runtime`、`hermes-runtime` 等资源不存在。
- 本地 kind 集群被重建或资源丢失。

## 本地创建 Team 时怎么选

在本地 kind 环境测试页面和 Team 功能时，建议创建 Team 成员时选择 `Pro` / 桌面实例模式。

如果选择 `Lite`，它会走 upstream 新的 runtime pool 调度，需要 `workspace-store` NFS 和 runtime pool Pod 都正常。本地 Docker Desktop / kind 可能无法满足这个条件，实例会停在 `Starting`。

## rollout 卡住时怎么确认原因

先看 app Pod：

```powershell
kubectl -n clawmanager-system get pods -l app=clawmanager-app -o wide
kubectl -n clawmanager-system describe pod -l app=clawmanager-app
```

如果看到类似下面的错误，就是 NFS 挂载问题：

```text
MountVolume.SetUp failed for volume "workspaces"
mount.nfs: Failed to resolve server workspace-store.clawmanager-system.svc.cluster.local
```

再看 workspace-store：

```powershell
kubectl -n clawmanager-system logs deploy/workspace-store --tail=120
```

如果看到：

```text
does not support NFS export
Export validation failed
```

说明本地 kind 不适合跑这套 Lite workspace-store NFS 链路。不要改后端逻辑绕过，应使用本地 overlay 让 app 不依赖这个挂载，并在本地优先测试 Pro 模式。

## 验证当前 app 镜像

```powershell
kubectl -n clawmanager-system get deploy clawmanager-app -o jsonpath="{.spec.template.spec.containers[0].image}"
```

应该显示：

```text
clawmanager:team-profiles-test
```

## 与服务器 tenant 部署的区别

本地 localhost 测试使用：

```text
deployments/k8s/clawmanager.yaml
deployments/k8s/clawmanager-team-profiles-test.yaml
namespace: clawmanager-system
port-forward: 30443:443
```

服务器多租户部署使用：

```text
deployments/k8s/clawmanager-tenant.yaml
deployments/k8s/clawmanager-apply.sh
namespace: clawmanager-hxc-system 或其他租户 namespace
NodePort: 32443 等
```

服务器部署应保留 upstream 的 runtime pool、NFS workspace、runtime scheduler 逻辑。本地测试 overlay 只服务于本地开发，不代表服务器部署方式。
docker build -t clawmanager:team-profiles-test .