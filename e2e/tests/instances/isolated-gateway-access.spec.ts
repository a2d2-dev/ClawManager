import type { APIRequestContext } from "@playwright/test";

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

function proxyURL(pathOrURL: string, path = "/") {
  const url = new URL(backendURL(pathOrURL));
  if (!url.pathname.endsWith("/")) {
    url.pathname += "/";
  }
  if (path !== "/") {
    url.pathname += path.replace(/^\/+/, "");
  }
  return url.toString();
}

async function gatewayHealth(request: APIRequestContext, instanceId: number, accessURL: string, token: string) {
  const response = await request.get(proxyURL(accessURL, "/health"), {
    headers: { Cookie: `instance_access_${instanceId}=${token}` },
    timeout: 30_000
  });
  const body = await response.text();
  return {
    status: response.status(),
    body
  };
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
      await expect(page.getByRole("main").getByText(/Sandbox .*\/.+/)).toBeVisible();
      await expect(page.locator("iframe")).toHaveCount(0);
      await expect(page.getByTitle(/fullscreen/i)).toHaveCount(0);
      await expect(page.getByText(/desktop stream/i)).toHaveCount(0);
      await expect(page.getByRole("button", { name: /share link/i })).toHaveCount(0);
      await expect(page.locator("[data-share-enabled]")).toHaveCount(0);

      const access = await generateInstanceAccessToken(request, tokens.access_token, instanceId);
      expect(access.token).toBeTruthy();
      expect(access.access_url || access.proxy_url).toContain(`/api/v1/instances/${instanceId}/proxy`);
      const accessURL = access.access_url || access.proxy_url;

      await expect
        .poll(
          async () => {
            const health = await gatewayHealth(request, instanceId!, accessURL, access.token);
            return health.status < 400 && /"ok"\s*:\s*true/.test(health.body) && /"status"\s*:\s*"live"/.test(health.body);
          },
          { timeout: 120_000, intervals: [2_000, 5_000, 10_000] }
        )
        .toBe(true);

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

      await expect
        .poll(
          async () => {
            const health = await gatewayHealth(request, instanceId!, accessURL, access.token);
            return health.status < 400 && /"ok"\s*:\s*true/.test(health.body) && /"status"\s*:\s*"live"/.test(health.body);
          },
          { timeout: 120_000, intervals: [2_000, 5_000, 10_000] }
        )
        .toBe(true);
    } finally {
      if (instanceId !== null) {
        await deleteInstance(request, tokens.access_token, instanceId).catch(() => undefined);
      }
    }
  });
});
