package services

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"clawreef/internal/models"
	"clawreef/internal/utils"

	"github.com/gin-gonic/gin"
)

type stubLLMModelRepository struct {
	active []models.LLMModel
}

func (r *stubLLMModelRepository) List() ([]models.LLMModel, error) {
	items := make([]models.LLMModel, len(r.active))
	copy(items, r.active)
	return items, nil
}

func (r *stubLLMModelRepository) ListActive() ([]models.LLMModel, error) {
	items := make([]models.LLMModel, len(r.active))
	copy(items, r.active)
	return items, nil
}

func (r *stubLLMModelRepository) GetByID(id int) (*models.LLMModel, error) {
	return nil, nil
}

func (r *stubLLMModelRepository) GetByDisplayName(displayName string) (*models.LLMModel, error) {
	return nil, nil
}

func (r *stubLLMModelRepository) Save(model *models.LLMModel) error {
	return nil
}

func (r *stubLLMModelRepository) Delete(id int) error {
	return nil
}

func TestResolveCreateRuntimeTypeRejectsInvalidModeRuntimeCombinations(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name string
		req  CreateInstanceRequest
	}{
		{name: "lite desktop", req: CreateInstanceRequest{Mode: InstanceModeLite, RuntimeType: RuntimeBackendDesktop}},
		{name: "lite shell", req: CreateInstanceRequest{Mode: InstanceModeLite, RuntimeType: RuntimeBackendShell}},
		{name: "pro gateway", req: CreateInstanceRequest{Mode: InstanceModePro, RuntimeType: RuntimeBackendGateway}},
		{name: "isolated desktop", req: CreateInstanceRequest{Mode: InstanceModeIsolated, RuntimeType: RuntimeBackendDesktop}},
		{name: "isolated shell", req: CreateInstanceRequest{Mode: InstanceModeIsolated, RuntimeType: RuntimeBackendShell}},
		{name: "mode instance_mode conflict", req: CreateInstanceRequest{Mode: InstanceModeLite, InstanceMode: InstanceModePro, RuntimeType: RuntimeBackendGateway}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mode, err := resolveCreateInstanceMode(tc.req)
			if err == nil {
				_, err = resolveCreateRuntimeType(tc.req, mode)
			}
			if err == nil {
				t.Fatal("expected invalid combination error")
			}
			if !strings.HasPrefix(err.Error(), "invalid instance mode/runtime_type combination:") {
				t.Fatalf("expected stable invalid combination prefix, got %q", err.Error())
			}
			assertHandleErrorStatus(t, err, http.StatusBadRequest)
		})
	}
}

func TestResolveCreateModeKeepsLegacyRuntimeTypeDefaultsValid(t *testing.T) {
	cases := []struct {
		runtimeType string
		wantMode    string
		wantRuntime string
	}{
		{runtimeType: RuntimeBackendGateway, wantMode: InstanceModeLite, wantRuntime: RuntimeBackendGateway},
		{runtimeType: RuntimeBackendDesktop, wantMode: InstanceModePro, wantRuntime: RuntimeBackendDesktop},
		{runtimeType: RuntimeBackendShell, wantMode: InstanceModePro, wantRuntime: RuntimeBackendShell},
	}

	for _, tc := range cases {
		t.Run(tc.runtimeType, func(t *testing.T) {
			req := CreateInstanceRequest{RuntimeType: tc.runtimeType}
			mode, err := resolveCreateInstanceMode(req)
			if err != nil {
				t.Fatalf("resolveCreateInstanceMode returned error: %v", err)
			}
			if mode != tc.wantMode {
				t.Fatalf("mode = %q, want %q", mode, tc.wantMode)
			}
			runtimeType, err := resolveCreateRuntimeType(req, mode)
			if err != nil {
				t.Fatalf("resolveCreateRuntimeType returned error: %v", err)
			}
			if runtimeType != tc.wantRuntime {
				t.Fatalf("runtimeType = %q, want %q", runtimeType, tc.wantRuntime)
			}
		})
	}
}

func TestInstanceDispatchRejectsEmptyAndInvalidStoredMode(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, tc := range []struct {
		name         string
		instanceMode string
	}{
		{name: "empty", instanceMode: ""},
		{name: "invalid", instanceMode: "mini"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			instance := &models.Instance{ID: 77, InstanceMode: tc.instanceMode}
			service := &instanceService{}
			_, _, _, err := service.runtimeBackendForInstance(instance)
			if err == nil {
				t.Fatal("expected dispatch error")
			}
			for _, want := range []string{"instance_id=77", fmt.Sprintf("instance_mode=%q", tc.instanceMode), "repair instance data"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("expected error %q to contain %q", err.Error(), want)
				}
			}
			assertHandleErrorStatus(t, err, http.StatusBadRequest)
		})
	}
}

func assertHandleErrorStatus(t *testing.T, err error, want int) {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	utils.HandleError(c, err)
	if recorder.Code != want {
		t.Fatalf("HandleError status = %d, want %d, body = %s", recorder.Code, want, recorder.Body.String())
	}
}

func TestBuildGatewayEnvInjectsGatewayModelCatalog(t *testing.T) {
	t.Setenv("CLAWMANAGER_LLM_GATEWAY_BASE_URL", "http://gateway.example/api/v1/gateway/llm")

	token := "igt_test_token"
	for _, instanceType := range []string{"openclaw", "hermes"} {
		t.Run(instanceType, func(t *testing.T) {
			service := &instanceService{
				llmModelRepo: &stubLLMModelRepository{
					active: []models.LLMModel{
						{DisplayName: "GPT-4.1"},
						{DisplayName: "Claude 3.7 Sonnet"},
						{DisplayName: "auto"},
						{ProviderModelName: "deepseek-r1"},
					},
				},
			}

			env, err := service.buildGatewayEnv(&models.Instance{
				Type:        instanceType,
				AccessToken: &token,
			})
			if err != nil {
				t.Fatalf("buildGatewayEnv returned error: %v", err)
			}

			if env["CLAWMANAGER_LLM_BASE_URL"] != "http://gateway.example/api/v1/gateway/llm" {
				t.Fatalf("expected CLAWMANAGER_LLM_BASE_URL to use gateway base URL, got %q", env["CLAWMANAGER_LLM_BASE_URL"])
			}
			if env["CLAWMANAGER_LLM_MODEL"] != `["auto","GPT-4.1","Claude 3.7 Sonnet","deepseek-r1"]` {
				t.Fatalf("expected CLAWMANAGER_LLM_MODEL to contain injected model catalog JSON, got %q", env["CLAWMANAGER_LLM_MODEL"])
			}
			if env["OPENAI_MODEL"] != "auto" {
				t.Fatalf("expected OPENAI_MODEL to remain the default gateway alias, got %q", env["OPENAI_MODEL"])
			}
			if env["CLAWMANAGER_LLM_API_KEY"] != token || env["OPENAI_API_KEY"] != token {
				t.Fatalf("expected gateway token aliases to be preserved")
			}
		})
	}
}

func TestBuildGatewayEnvEnsuresMissingGatewayToken(t *testing.T) {
	t.Setenv("CLAWMANAGER_LLM_GATEWAY_BASE_URL", "http://gateway.example/api/v1/gateway/llm")

	instanceRepo := &stubGatewayEnvInstanceRepository{fakeRuntimeInstanceRepo: newFakeRuntimeInstanceRepo()}
	service := &instanceService{
		instanceRepo: instanceRepo,
		llmModelRepo: &stubLLMModelRepository{
			active: []models.LLMModel{{DisplayName: "auto"}},
		},
	}
	instance := &models.Instance{
		ID:     68,
		UserID: 1,
		Type:   "openclaw",
	}

	env, err := service.BuildGatewayEnv(instance)
	if err != nil {
		t.Fatalf("BuildGatewayEnv returned error: %v", err)
	}
	if instance.AccessToken == nil || *instance.AccessToken == "" {
		t.Fatal("BuildGatewayEnv did not provision instance access token")
	}
	if env["CLAWMANAGER_LLM_API_KEY"] != *instance.AccessToken {
		t.Fatalf("CLAWMANAGER_LLM_API_KEY = %q, want provisioned token %q", env["CLAWMANAGER_LLM_API_KEY"], *instance.AccessToken)
	}
	if got := instanceRepo.updated[68]; got == nil || got.AccessToken == nil || *got.AccessToken != *instance.AccessToken {
		t.Fatalf("repository update did not persist provisioned token: %#v", instanceRepo.updated[68])
	}
}

func TestBuildGatewayEnvMergesEnvironmentOverrides(t *testing.T) {
	t.Setenv("CLAWMANAGER_LLM_GATEWAY_BASE_URL", "http://gateway.example/api/v1/gateway/llm")
	token := "igt_test_token"
	raw, err := marshalEnvironmentOverrides(map[string]string{
		"CLAWMANAGER_TEAM_ENABLED":   "true",
		"CLAWMANAGER_TEAM_MEMBER_ID": "lite-worker",
		"CUSTOM_GATEWAY_ENV":         "enabled",
	})
	if err != nil {
		t.Fatalf("marshalEnvironmentOverrides returned error: %v", err)
	}
	service := &instanceService{
		llmModelRepo: &stubLLMModelRepository{
			active: []models.LLMModel{{DisplayName: "auto"}},
		},
	}

	env, err := service.BuildGatewayEnv(&models.Instance{
		ID:                       88,
		Type:                     "openclaw",
		RuntimeType:              RuntimeBackendGateway,
		AccessToken:              &token,
		EnvironmentOverridesJSON: raw,
	})
	if err != nil {
		t.Fatalf("BuildGatewayEnv returned error: %v", err)
	}

	if env["CLAWMANAGER_TEAM_ENABLED"] != "true" || env["CLAWMANAGER_TEAM_MEMBER_ID"] != "lite-worker" {
		t.Fatalf("expected Team environment overrides to be merged into gateway env, got %#v", env)
	}
	if env["CUSTOM_GATEWAY_ENV"] != "enabled" {
		t.Fatalf("expected custom gateway environment override to be merged, got %#v", env)
	}
	if env["CLAWMANAGER_LLM_API_KEY"] != token {
		t.Fatalf("expected gateway token env to remain available")
	}
	if env["CLAWMANAGER_RUNTIME_TYPE"] != RuntimeBackendGateway {
		t.Fatalf("expected runtime type marker %q, got %q", RuntimeBackendGateway, env["CLAWMANAGER_RUNTIME_TYPE"])
	}
}

func TestBuildGatewayEnvSkipsUnmanagedRuntime(t *testing.T) {
	token := "igt_test_token"
	service := &instanceService{}

	env, err := service.buildGatewayEnv(&models.Instance{
		Type:        "ubuntu",
		AccessToken: &token,
	})
	if err != nil {
		t.Fatalf("buildGatewayEnv returned error: %v", err)
	}
	if len(env) != 0 {
		t.Fatalf("expected unmanaged runtime to receive no gateway env, got %#v", env)
	}
}

type stubGatewayEnvInstanceRepository struct {
	*fakeRuntimeInstanceRepo
	updated map[int]*models.Instance
}

func (r *stubGatewayEnvInstanceRepository) Update(instance *models.Instance) error {
	if r.updated == nil {
		r.updated = map[int]*models.Instance{}
	}
	copy := *instance
	r.updated[instance.ID] = &copy
	return nil
}

func TestBuildAgentEnvInjectsHermesAgentConfig(t *testing.T) {
	t.Setenv("CLAWMANAGER_AGENT_CONTROL_BASE_URL", "http://agent-control.example")

	token := "agt_boot_test_token"
	service := &instanceService{}

	env, err := service.buildAgentEnv(&models.Instance{
		ID:                  24,
		Type:                "hermes",
		DiskGB:              20,
		AgentBootstrapToken: &token,
	})
	if err != nil {
		t.Fatalf("buildAgentEnv returned error: %v", err)
	}

	if env["CLAWMANAGER_AGENT_ENABLED"] != "true" {
		t.Fatalf("expected Hermes agent to be enabled")
	}
	if env["CLAWMANAGER_AGENT_BASE_URL"] != "http://agent-control.example" {
		t.Fatalf("expected Hermes agent base URL to be injected, got %q", env["CLAWMANAGER_AGENT_BASE_URL"])
	}
	if env["CLAWMANAGER_AGENT_BOOTSTRAP_TOKEN"] != token {
		t.Fatalf("expected Hermes agent bootstrap token to be injected")
	}
	if env["CLAWMANAGER_AGENT_INSTANCE_ID"] != "24" {
		t.Fatalf("expected Hermes instance id to be injected, got %q", env["CLAWMANAGER_AGENT_INSTANCE_ID"])
	}
	if env["CLAWMANAGER_AGENT_PERSISTENT_DIR"] != "/config/.hermes" {
		t.Fatalf("expected Hermes persistent dir /config/.hermes, got %q", env["CLAWMANAGER_AGENT_PERSISTENT_DIR"])
	}
	if env["CLAWMANAGER_AGENT_DISK_LIMIT_BYTES"] != "21474836480" {
		t.Fatalf("expected Hermes disk limit bytes to be injected, got %q", env["CLAWMANAGER_AGENT_DISK_LIMIT_BYTES"])
	}
}

func TestPersistentVolumeMountPathNormalizesManagedDesktopRuntimes(t *testing.T) {
	for _, instanceType := range []string{"openclaw", "ubuntu", "webtop", "hermes"} {
		t.Run(instanceType, func(t *testing.T) {
			got := persistentVolumeMountPath(&models.Instance{
				Type:      instanceType,
				MountPath: "/data",
			})
			if got != "/config" {
				t.Fatalf("expected %s PVC mount path /config, got %q", instanceType, got)
			}
		})
	}
}

func TestManagedRuntimePersistentDirKeepsHermesSubdirectory(t *testing.T) {
	got := managedRuntimePersistentDir(&models.Instance{
		Type:      "hermes",
		MountPath: "/config",
	})
	if got != "/config/.hermes" {
		t.Fatalf("expected Hermes persistent dir /config/.hermes, got %q", got)
	}
}

func TestRuntimeVolumeInitScriptsAddsHermesLayoutMigration(t *testing.T) {
	scripts := runtimeVolumeInitScripts("hermes", "/config")
	if len(scripts) != 1 {
		t.Fatalf("expected one Hermes volume init script, got %d", len(scripts))
	}
	if scripts[0].Name != "data" || scripts[0].MountPath != "/config" {
		t.Fatalf("unexpected Hermes volume init script: %#v", scripts[0])
	}
	if !strings.Contains(scripts[0].Script, `target="$base/.hermes"`) {
		t.Fatalf("expected Hermes init script to target /config/.hermes, got %s", scripts[0].Script)
	}
}

func TestResolveGatewayModelInjectionRequiresActiveModels(t *testing.T) {
	service := &instanceService{
		llmModelRepo: &stubLLMModelRepository{},
	}

	injection, err := service.resolveGatewayModelInjection()
	if err == nil {
		t.Fatalf("expected resolveGatewayModelInjection to fail when no active models exist, got %#v", injection)
	}
}

func TestSecurityModeForInstance(t *testing.T) {
	service := &instanceService{}

	if got := service.securityModeForInstance("openclaw"); got != "chromium-compat" {
		t.Fatalf("expected openclaw to use chromium compat mode, got %q", got)
	}
	if got := service.securityModeForInstance("ubuntu"); got != "default" {
		t.Fatalf("expected ubuntu to use default security mode, got %q", got)
	}

	service.allowPrivilegedPods = true
	if got := service.securityModeForInstance("openclaw"); got != "privileged" {
		t.Fatalf("expected explicit privileged override to win, got %q", got)
	}
}
