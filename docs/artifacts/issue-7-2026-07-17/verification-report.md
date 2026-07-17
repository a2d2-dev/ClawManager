# PR #20 Isolated 三向模式选择器浏览器实拍验证

日期：2026-07-17

分支：`docs/artifacts-issue-7-ui-evidence`

验证对象：`f26ad61` (`feat: surface Isolated mode with capability gating (three-value instance mode) (#20)`)

## 实验目的

验证 PR #20 合并后，创建实例页在真实浏览器中展示 Lite / Isolated Gateway / Pro 三向模式选择器；当集群具备 `agent-sandbox` Sandbox CRD 时，Isolated Gateway 可被选中；中文 locale 下独立网关文案正确；提交 isolated 创建请求时，当前未交付后端按预期返回显式错误。

## 环境与启动记录

- 工作目录：`/data/src/github.com/a2d2-dev/ClawManager/.worktrees/issue-7-isolated-gating`
- 前端：Vite，`http://10.126.126.12:19002/`
- 后端：Go server，`http://10.126.126.12:19001/`
- API：前端通过 `VITE_API_URL=http://10.126.126.12:19001/api/v1` 访问本地后端
- 数据库：复用 dev 集群 `clawmanager-system/mysql`，通过 `kubectl port-forward --address 0.0.0.0 svc/mysql 13308:3306` 连接
- Kubeconfig：`/root/.kube/hwneov.config`
- 浏览器工具：`agent-browser`

能力探测记录：

```text
kubectl --kubeconfig /root/.kube/hwneov.config get crd sandboxes.agents.x-k8s.io -o name
customresourcedefinition.apiextensions.k8s.io/sandboxes.agents.x-k8s.io

backend log:
Runtime capability detected: isolated mode available
```

## 操作步骤

1. 用 `agent-browser` 打开 `http://10.126.126.12:19002/login`。
2. 因集群默认 `admin/admin123` 已不可用，通过 UI 注册临时账号 `pr20evidence0717` 并自动登录。
3. 进入创建实例页，截取默认 Lite 选中态和三向选择器。
4. 选择 Isolated Gateway，截取英文选中态。
5. 设置 `clawmanager_locale=zh` 并刷新，选择“独立网关”，截取中文 locale 文案与选中态。
6. 以 isolated 模式填写实例名 `isolated-evidence-0717`，保留默认 OpenClaw 类型，提交创建请求。
7. 观察 UI 错误提示和后端日志。

## 验证记录

- 三向选择器展示 Lite / Isolated Gateway / Pro，默认 Lite 高亮。
- Isolated Gateway 在当前集群能力可用时没有禁用，点击后高亮切换到 Isolated Gateway。
- 中文 locale 下显示“独立网关”和“独享无桌面 Gateway 运行时，需要集群安装 agent-sandbox Sandbox CRD。”。
- isolated 创建请求提交后，UI 显示错误：

```text
isolated runtime backend is not delivered yet; follow-up issue #8 provides sandboxBackend
```

后端同步记录：

```text
[ERROR] isolated runtime backend is not delivered yet; follow-up issue #8 provides sandboxBackend
POST "/api/v1/instances" -> 500
```

## 截图证据

- `01-create-page-three-mode-default-lite.png`：三向选择器完整创建页，默认 Lite 选中
- `02-create-page-isolated-selected.png`：Isolated Gateway 选中态
- `03-create-page-zh-isolated-copy.png`：中文 locale 下“独立网关”选中态与文案
- `04-isolated-create-not-delivered-error.png`：提交 isolated 创建请求后的显式错误

## 备注

- 本次验证没有使用 Playwright；所有页面操作和截图均通过 `agent-browser` 执行。
- 本地一次性 MySQL 容器初始化过慢，未作为最终验证数据库；最终验证使用 dev 集群现有 MySQL 服务。
- 页面中存在隐藏的重复按钮节点，`agent-browser` 的 ref/text click 多次命中不可见副本；第 2 步跳转与最终提交使用 `agent-browser eval` 在真实页面内点击可见按钮完成。
