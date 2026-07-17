import { expect, test } from "../../fixtures/test.js";
import { env } from "../../fixtures/env.js";
import {
  createInstance,
  deleteInstance,
  generateInstanceAccessToken,
  getInstance,
  getInstanceStatus,
  login,
  startInstance,
  stopInstance
} from "../../fixtures/apiClient.js";
import { users } from "../../fixtures/users.js";

test.use({ storageState: users.admin.storageState });

function backendOrigin() {
  return env.backendUrl.replace(/\/api\/v1\/?$/, "");
}

function backendURL(pathOrURL: string) {
  return new URL(pathOrURL, backendOrigin()).toString();
}

test.describe("isolated instance gateway access", () => {
  test("@p0 create isolated instance, reach gateway, and survive stop/start", async ({ page, request }) => {
    test.setTimeout(8 * 60 * 1000);

    const tokens = await login(request, users.admin);
    const instanceName = `e2e-isolated-${Date.now()}`;
    let instanceId: number | null = null;

    try {
      const created = await createInstance(request, tokens.access_token, {
        name: instanceName,
        type: "openclaw",
        mode: "isolated",
        instance_mode: "isolated",
        runtime_type: "gateway",
        cpu_cores: 2,
        memory_gb: 4,
        disk_gb: 20,
        gpu_enabled: false,
        gpu_count: 0,
        os_type: "ubuntu",
        os_version: "latest"
      });
      instanceId = created.id;
      expect(created.instance_mode).toBe("isolated");
      expect(created.runtime_type).toBe("gateway");

      await expect
        .poll(
          async () => {
            const status = await getInstanceStatus(request, tokens.access_token, instanceId!);
            return {
              status: status.status,
              namespace: status.pod_namespace,
              name: status.pod_name
            };
          },
          { timeout: 240_000, intervals: [2_000, 5_000, 10_000] }
        )
        .toMatchObject({ status: "running" });

      const beforeRestart = await getInstance(request, tokens.access_token, instanceId);
      const originalWorkspacePath = beforeRestart.workspace_path;

      await page.goto(`/instances/${instanceId}`);
      await expect(page.getByRole("heading", { name: instanceName }).first()).toBeVisible();
      await expect.poll(() => page.locator("body").innerText()).toContain("Mode Isolated");
      await expect.poll(() => page.locator("body").innerText()).toContain("Runtime gateway");
      await expect(page.getByRole("heading", { name: /runtime backend/i })).toBeVisible();
      await expect(page.getByText(/Sandbox .*\/.+/)).toBeVisible();
      await expect(page.locator("iframe")).toHaveCount(0);
      await expect(page.getByTitle(/fullscreen/i)).toHaveCount(0);
      await expect(page.getByText(/desktop stream/i)).toHaveCount(0);

      const access = await generateInstanceAccessToken(request, tokens.access_token, instanceId);
      expect(access.token).toBeTruthy();
      expect(access.access_url || access.proxy_url).toContain(`/api/v1/instances/${instanceId}/proxy`);

      await expect
        .poll(
          async () => {
            const response = await request.get(backendURL(access.access_url || access.proxy_url), {
              headers: { Cookie: `instance_access_${instanceId}=${access.token}` },
              timeout: 30_000
            });
            return response.status();
          },
          { timeout: 120_000, intervals: [2_000, 5_000, 10_000] }
        )
        .toBeLessThan(400);

      await stopInstance(request, tokens.access_token, instanceId);
      await expect
        .poll(async () => (await getInstanceStatus(request, tokens.access_token, instanceId!)).status, {
          timeout: 120_000,
          intervals: [2_000, 5_000, 10_000]
        })
        .toBe("stopped");

      await startInstance(request, tokens.access_token, instanceId);
      await expect
        .poll(async () => (await getInstanceStatus(request, tokens.access_token, instanceId!)).status, {
          timeout: 240_000,
          intervals: [2_000, 5_000, 10_000]
        })
        .toBe("running");

      const afterRestart = await getInstance(request, tokens.access_token, instanceId);
      expect(afterRestart.instance_mode).toBe("isolated");
      expect(afterRestart.runtime_type).toBe("gateway");
      expect(afterRestart.workspace_path).toBe(originalWorkspacePath);
    } finally {
      if (instanceId !== null) {
        await deleteInstance(request, tokens.access_token, instanceId).catch(() => undefined);
      }
    }
  });
});
