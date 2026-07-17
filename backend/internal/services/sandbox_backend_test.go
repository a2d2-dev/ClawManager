package services

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"clawreef/internal/models"
	"clawreef/internal/services/k8s"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestBuildIsolatedSandboxObjectRendersSpecAndForcesProxyEnv(t *testing.T) {
	rawOverrides, err := marshalEnvironmentOverrides(map[string]string{
		"CUSTOM_ENV":             "custom",
		"HTTP_PROXY":             "http://override.invalid:3128",
		"SUBFOLDER":              "/desktop",
		"SELKIES_ENCODER":        "jpeg",
		"KASM_SVC_SEND_CUT_TEXT": "1",
	})
	if err != nil {
		t.Fatalf("marshal overrides: %v", err)
	}
	accessToken := "igt_test"
	agentToken := "agt_boot_test"
	workspacePath := "/workspaces/openclaw/user-45/instance-101"
	instance := &models.Instance{
		ID:                       101,
		UserID:                   45,
		Name:                     "Isolated Dev",
		Type:                     RuntimeTypeOpenClaw,
		RuntimeType:              RuntimeBackendGateway,
		InstanceMode:             InstanceModeIsolated,
		CPUCores:                 2.5,
		MemoryGB:                 4,
		DiskGB:                   20,
		GPUEnabled:               true,
		GPUCount:                 1,
		EnvironmentOverridesJSON: rawOverrides,
		AccessToken:              &accessToken,
		AgentBootstrapToken:      &agentToken,
		WorkspacePath:            &workspacePath,
	}

	sandbox, err := buildIsolatedSandboxObject(isolatedSandboxSpec{
		Instance:     instance,
		Name:         "clawreef-101-isolated-dev",
		Namespace:    "clawreef-user-45",
		Image:        "registry/openclaw-lite:latest",
		RuntimeEnv:   isolatedBaseRuntimeEnv(instance),
		GatewayEnv:   map[string]string{"OPENAI_API_KEY": "model-token", "CLAWMANAGER_INSTANCE_TOKEN": accessToken},
		AgentEnv:     map[string]string{"CLAWMANAGER_AGENT_ENABLED": "true", "CLAWMANAGER_AGENT_BOOTSTRAP_TOKEN": agentToken},
		ProxyURL:     "http://proxy.good:3128",
		StorageClass: "manual",
		Placement: &RuntimePlacement{
			NodeSelector: RuntimeNodeSelector{
				MatchLabels: map[string]string{"nodepool": "isolated"},
				MatchExpressions: []corev1.NodeSelectorRequirement{{
					Key:      "topology.kubernetes.io/zone",
					Operator: corev1.NodeSelectorOpIn,
					Values:   []string{"zone-a", "zone-b"},
				}},
			},
			RuntimeClassName: "kata",
			Tolerations: []corev1.Toleration{{
				Key:      "dedicated",
				Operator: corev1.TolerationOpEqual,
				Value:    "sandbox",
				Effect:   corev1.TaintEffectNoSchedule,
			}},
		},
	})
	if err != nil {
		t.Fatalf("buildIsolatedSandboxObject returned error: %v", err)
	}

	if got := sandbox.GetAPIVersion(); got != sandboxAPIVersion {
		t.Fatalf("apiVersion = %q, want %q", got, sandboxAPIVersion)
	}
	if got := sandbox.GetAnnotations()["clawmanager.io/agent-sandbox-version"]; got != agentSandboxVersionNote {
		t.Fatalf("agent-sandbox version annotation = %q, want %q", got, agentSandboxVersionNote)
	}
	if service, _, _ := unstructured.NestedBool(sandbox.Object, "spec", "service"); !service {
		t.Fatalf("spec.service = false, want true")
	}
	if mode, _, _ := unstructured.NestedString(sandbox.Object, "spec", "operatingMode"); mode != sandboxOperatingModeRunning {
		t.Fatalf("operatingMode = %q, want Running", mode)
	}

	annotations, _, _ := unstructured.NestedStringMap(sandbox.Object, "spec", "podTemplate", "metadata", "annotations")
	if annotations["prometheus.io/scrape"] != "true" || annotations["prometheus.io/path"] != "/metrics" || annotations["prometheus.io/port"] != isolatedAgentPublicPort {
		t.Fatalf("prometheus annotations = %#v", annotations)
	}
	nodeSelector, _, _ := unstructured.NestedStringMap(sandbox.Object, "spec", "podTemplate", "spec", "nodeSelector")
	if nodeSelector["nodepool"] != "isolated" {
		t.Fatalf("nodeSelector = %#v", nodeSelector)
	}
	runtimeClassName, _, _ := unstructured.NestedString(sandbox.Object, "spec", "podTemplate", "spec", "runtimeClassName")
	if runtimeClassName != "kata" {
		t.Fatalf("runtimeClassName = %q, want kata", runtimeClassName)
	}
	expressionKey, _, _ := unstructured.NestedString(sandbox.Object, "spec", "podTemplate", "spec", "affinity", "nodeAffinity", "requiredDuringSchedulingIgnoredDuringExecution", "nodeSelectorTerms", "0", "matchExpressions", "0", "key")
	if expressionKey != "" {
		t.Fatalf("unexpected direct indexed expression lookup = %q", expressionKey)
	}
	expressions := nestedSlice(t, sandbox, "spec", "podTemplate", "spec", "affinity", "nodeAffinity", "requiredDuringSchedulingIgnoredDuringExecution", "nodeSelectorTerms")
	firstTerm := expressions[0].(map[string]interface{})
	matchExpressions := firstTerm["matchExpressions"].([]interface{})
	firstExpression := matchExpressions[0].(map[string]interface{})
	if firstExpression["key"] != "topology.kubernetes.io/zone" || firstExpression["operator"] != string(corev1.NodeSelectorOpIn) {
		t.Fatalf("matchExpressions = %#v", firstExpression)
	}
	tolerations := nestedSlice(t, sandbox, "spec", "podTemplate", "spec", "tolerations")
	if tolerations[0].(map[string]interface{})["key"] != "dedicated" {
		t.Fatalf("tolerations = %#v", tolerations)
	}

	containers := nestedSlice(t, sandbox, "spec", "podTemplate", "spec", "containers")
	container := containers[0].(map[string]interface{})
	if container["name"] != RuntimeTypeOpenClaw || container["image"] != "registry/openclaw-lite:latest" {
		t.Fatalf("container identity = %#v", container)
	}
	command := strings.Join(stringSliceFromInterface(container["command"]), " ")
	if !strings.Contains(command, "openclaw gateway run") {
		t.Fatalf("command = %q, want gateway-only openclaw command", command)
	}
	env := envMapFromContainer(t, container)
	if env["HTTP_PROXY"] != "http://proxy.good:3128" || env["HTTPS_PROXY"] != "http://proxy.good:3128" {
		t.Fatalf("proxy env was not forced: %#v", env)
	}
	if env["CUSTOM_ENV"] != "custom" || env["OPENAI_API_KEY"] != "model-token" || env["CLAWMANAGER_AGENT_ENABLED"] != "true" {
		t.Fatalf("governed/custom env missing: %#v", env)
	}
	for _, key := range []string{"SUBFOLDER", "TITLE", "SELKIES_ENCODER", "KASM_SVC_SEND_CUT_TEXT"} {
		if _, ok := env[key]; ok {
			t.Fatalf("desktop env %s must not be present in isolated sandbox env: %#v", key, env)
		}
	}
	limits := container["resources"].(map[string]interface{})["limits"].(map[string]interface{})
	if limits["memory"] != "4Gi" || limits["nvidia.com/gpu"] != "1" {
		t.Fatalf("resource limits = %#v", limits)
	}
	volumeTemplates := nestedSlice(t, sandbox, "spec", "volumeClaimTemplates")
	workspace := volumeTemplates[0].(map[string]interface{})
	if workspace["metadata"].(map[string]interface{})["name"] != "workspace" {
		t.Fatalf("first volumeClaimTemplate = %#v, want workspace", workspace)
	}
	storage := workspace["spec"].(map[string]interface{})["resources"].(map[string]interface{})["requests"].(map[string]interface{})["storage"]
	if storage != "20Gi" {
		t.Fatalf("workspace storage = %v, want 20Gi", storage)
	}
	if workspace["spec"].(map[string]interface{})["storageClassName"] != "manual" {
		t.Fatalf("workspace storageClassName = %#v", workspace["spec"].(map[string]interface{})["storageClassName"])
	}
}

func TestSandboxBackendCreateCreatesSandboxSpecWithGovernedEnv(t *testing.T) {
	t.Setenv("CLAWMANAGER_LLM_GATEWAY_BASE_URL", "http://gateway.example/api/v1/gateway/llm")
	t.Setenv("CLAWMANAGER_AGENT_CONTROL_BASE_URL", "http://agent-control.example")

	instanceRepo := newV2LifecycleInstanceRepo()
	dynamicClient := dynamicfake.NewSimpleDynamicClient(kruntime.NewScheme())
	backend := newSandboxBackendForTest(instanceRepo, dynamicClient)
	precheckCalls := 0
	backend.proxyPrecheck = func(context.Context, string) error {
		precheckCalls++
		return nil
	}

	instance, err := backend.Create(context.Background(), 45, CreateInstanceRequest{
		Name:                 "Created Spec",
		Type:                 "openclaw",
		CPUCores:             1.5,
		MemoryGB:             3,
		DiskGB:               25,
		OSType:               "openclaw",
		OSVersion:            "latest",
		StorageClass:         "manual",
		EnvironmentOverrides: map[string]string{"CUSTOM_ENV": "from-create"},
		Placement: &RuntimePlacement{NodeSelector: RuntimeNodeSelector{MatchLabels: map[string]string{
			"nodepool": "isolated",
		}}},
	}, InstanceModeIsolated, RuntimeBackendGateway, mustMarshalEnvOverrides(t, map[string]string{"CUSTOM_ENV": "from-create"}))
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if precheckCalls != 1 {
		t.Fatalf("proxy precheck calls = %d, want 1", precheckCalls)
	}
	if instance.ID == 0 || instance.AccessToken == nil || instance.AgentBootstrapToken == nil {
		t.Fatalf("expected persisted instance tokens, got %#v", instance)
	}
	if instance.WorkspacePath == nil || *instance.WorkspacePath != "/workspaces/openclaw/user-45/instance-1" {
		t.Fatalf("workspace path = %#v", instance.WorkspacePath)
	}

	sandbox, err := dynamicClient.Resource(sandboxGVR).Namespace("clawreef-user-45").Get(context.Background(), "clawreef-1-created-spec", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get created sandbox: %v", err)
	}
	annotations, _, _ := unstructured.NestedStringMap(sandbox.Object, "spec", "podTemplate", "metadata", "annotations")
	if annotations["prometheus.io/path"] != "/metrics" || annotations["prometheus.io/port"] != isolatedAgentPublicPort {
		t.Fatalf("prometheus annotations = %#v", annotations)
	}
	containers := nestedSlice(t, sandbox, "spec", "podTemplate", "spec", "containers")
	env := envMapFromContainer(t, containers[0].(map[string]interface{}))
	if env["HTTP_PROXY"] != "http://proxy.good:3128" || env["OPENAI_MODEL"] != "auto" || env["CLAWMANAGER_AGENT_ENABLED"] != "true" || env["CUSTOM_ENV"] != "from-create" {
		t.Fatalf("created sandbox env missing governed/proxy/custom values: %#v", env)
	}
	nodeSelector, _, _ := unstructured.NestedStringMap(sandbox.Object, "spec", "podTemplate", "spec", "nodeSelector")
	if nodeSelector["nodepool"] != "isolated" {
		t.Fatalf("nodeSelector = %#v", nodeSelector)
	}
	volumeTemplates := nestedSlice(t, sandbox, "spec", "volumeClaimTemplates")
	workspace := volumeTemplates[0].(map[string]interface{})
	storage := workspace["spec"].(map[string]interface{})["resources"].(map[string]interface{})["requests"].(map[string]interface{})["storage"]
	if storage != "25Gi" {
		t.Fatalf("workspace storage = %v, want 25Gi", storage)
	}
}

func TestIsolatedReservedProxyEnvRejectedOnCreateAndUpdate(t *testing.T) {
	instanceRepo := newV2LifecycleInstanceRepo()
	service := &instanceService{
		instanceRepo:        instanceRepo,
		quotaRepo:           v2LifecycleQuotaRepo{},
		runtimeCapabilities: isolatedAvailableCapabilities(),
	}
	_, err := service.Create(45, CreateInstanceRequest{
		Name:                 "Bad Proxy",
		Type:                 "openclaw",
		Mode:                 InstanceModeIsolated,
		RuntimeType:          RuntimeBackendGateway,
		CPUCores:             2,
		MemoryGB:             4,
		DiskGB:               20,
		OSType:               "openclaw",
		OSVersion:            "latest",
		EnvironmentOverrides: map[string]string{"HTTP_PROXY": "http://bad"},
	})
	if err == nil || !strings.Contains(err.Error(), "reserved proxy environment variable HTTP_PROXY") {
		t.Fatalf("Create error = %v, want reserved proxy rejection", err)
	}
	if len(instanceRepo.created) != 0 {
		t.Fatalf("reserved env create must not persist instance, got %#v", instanceRepo.created)
	}

	instanceRepo.byID[77] = &models.Instance{ID: 77, UserID: 45, Name: "Existing", Type: "openclaw", RuntimeType: RuntimeBackendGateway, InstanceMode: InstanceModeIsolated}
	overrides := map[string]string{"HTTPS_PROXY": "http://bad"}
	err = service.Update(77, UpdateInstanceRequest{EnvironmentOverrides: &overrides})
	if err == nil || !strings.Contains(err.Error(), "reserved proxy environment variable HTTPS_PROXY") {
		t.Fatalf("Update error = %v, want reserved proxy rejection", err)
	}
}

func TestSandboxBackendCreateRefusesWhenProxyPrecheckFails(t *testing.T) {
	instanceRepo := newV2LifecycleInstanceRepo()
	backend := newSandboxBackendForTest(instanceRepo, nil)
	backend.proxyPrecheck = func(context.Context, string) error {
		return egressProxyUnreachable("http://proxy.bad:3128", errors.New("dial failed"))
	}

	_, err := backend.Create(context.Background(), 45, CreateInstanceRequest{
		Name:      "Precheck Fails",
		Type:      "openclaw",
		CPUCores:  2,
		MemoryGB:  4,
		DiskGB:    20,
		OSType:    "openclaw",
		OSVersion: "latest",
	}, InstanceModeIsolated, RuntimeBackendGateway, nil)
	if err == nil || !strings.Contains(err.Error(), EgressProxyUnreachableCode) {
		t.Fatalf("Create error = %v, want egress proxy unreachable", err)
	}
	if len(instanceRepo.created) != 0 {
		t.Fatalf("precheck refusal must happen before DB create, got %#v", instanceRepo.created)
	}
}

func TestSandboxBackendStartRefusesWhenProxyPrecheckFails(t *testing.T) {
	instance := sandboxTestInstance(87, "stopped")
	sandbox := sandboxObjectForTest(instance, nil)
	instanceRepo := newV2LifecycleInstanceRepo()
	instanceRepo.byID[instance.ID] = instance
	backend := newSandboxBackendForTest(instanceRepo, newDynamicClientWithSandboxes(t, sandbox))
	backend.proxyPrecheck = func(context.Context, string) error {
		return egressProxyUnreachable("http://proxy.bad:3128", errors.New("dial failed"))
	}

	err := backend.Start(context.Background(), instance, RuntimeBackendGateway)
	if err == nil || !strings.Contains(err.Error(), EgressProxyUnreachableCode) {
		t.Fatalf("Start error = %v, want egress proxy unreachable", err)
	}
	if _, ok := instanceRepo.runtimeStates[87]; ok {
		t.Fatalf("Start precheck refusal must not update runtime state: %#v", instanceRepo.runtimeStates[87])
	}
}

func TestSandboxBackendCreateUnavailableWhenProxyURLMissing(t *testing.T) {
	instanceRepo := newV2LifecycleInstanceRepo()
	backend := newSandboxBackendForTest(instanceRepo, nil)
	backend.proxyURL = func() (string, bool) { return "", false }

	_, err := backend.Create(context.Background(), 45, CreateInstanceRequest{
		Name:      "No Proxy",
		Type:      "openclaw",
		CPUCores:  2,
		MemoryGB:  4,
		DiskGB:    20,
		OSType:    "openclaw",
		OSVersion: "latest",
	}, InstanceModeIsolated, RuntimeBackendGateway, nil)
	if err == nil || !strings.Contains(err.Error(), "mode unavailable") || !strings.Contains(err.Error(), "egress proxy URL") {
		t.Fatalf("Create error = %v, want explicit missing proxy URL error", err)
	}
}

func TestSandboxBackendLifecyclePatchesOperatingMode(t *testing.T) {
	instance := sandboxTestInstance(88, "running")
	sandbox := sandboxObjectForTest(instance, nil)
	instanceRepo := newV2LifecycleInstanceRepo()
	instanceRepo.byID[instance.ID] = instance
	dynamicClient := newDynamicClientWithSandboxes(t, sandbox)
	backend := newSandboxBackendForTest(instanceRepo, dynamicClient)

	if err := backend.Stop(context.Background(), instance); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
	got, err := dynamicClient.Resource(sandboxGVR).Namespace("clawreef-user-45").Get(context.Background(), "clawreef-88-isolated", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get sandbox after stop: %v", err)
	}
	if mode, _, _ := unstructured.NestedString(got.Object, "spec", "operatingMode"); mode != sandboxOperatingModeSuspended {
		t.Fatalf("operatingMode after Stop = %q, want Suspended", mode)
	}
	if instanceRepo.byID[88].Status != "stopped" {
		t.Fatalf("instance status after Stop = %q, want stopped", instanceRepo.byID[88].Status)
	}

	if err := backend.Start(context.Background(), instance, RuntimeBackendGateway); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	got, err = dynamicClient.Resource(sandboxGVR).Namespace("clawreef-user-45").Get(context.Background(), "clawreef-88-isolated", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get sandbox after start: %v", err)
	}
	if mode, _, _ := unstructured.NestedString(got.Object, "spec", "operatingMode"); mode != sandboxOperatingModeRunning {
		t.Fatalf("operatingMode after Start = %q, want Running", mode)
	}
	if instanceRepo.byID[88].Status != "creating" {
		t.Fatalf("instance status after Start = %q, want creating", instanceRepo.byID[88].Status)
	}
}

func TestSandboxBackendStatusUsesLatestConditionForStaleSuspended(t *testing.T) {
	instance := sandboxTestInstance(89, "creating")
	conditions := []any{
		map[string]any{"type": "Suspended", "status": "True", "reason": "PodTerminated", "observedGeneration": int64(3), "lastTransitionTime": "2026-07-17T03:00:00Z"},
		map[string]any{"type": "Ready", "status": "True", "reason": "DependenciesReady", "observedGeneration": int64(4), "lastTransitionTime": "2026-07-17T03:02:00Z"},
	}
	sandbox := sandboxObjectForTest(instance, conditions)
	instanceRepo := newV2LifecycleInstanceRepo()
	instanceRepo.byID[instance.ID] = instance
	backend := newSandboxBackendForTest(instanceRepo, newDynamicClientWithSandboxes(t, sandbox))

	status, err := backend.Status(context.Background(), instance)
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if status.Status != "running" {
		t.Fatalf("status = %q, want running", status.Status)
	}
	if instanceRepo.byID[89].Status != "running" {
		t.Fatalf("repo status = %q, want running", instanceRepo.byID[89].Status)
	}
}

func TestSandboxBackendStatusMapsSuspendedAndFinishedConditions(t *testing.T) {
	suspended := sandboxStateFromConditions(&unstructured.Unstructured{Object: map[string]any{
		"status": map[string]any{"conditions": []any{
			map[string]any{"type": "Ready", "status": "True", "reason": "DependenciesReady", "observedGeneration": int64(1)},
			map[string]any{"type": "Suspended", "status": "True", "reason": "SandboxSuspended", "observedGeneration": int64(2)},
		}},
	}})
	if suspended.Status != "stopped" {
		t.Fatalf("suspended status = %q, want stopped", suspended.Status)
	}

	finished := sandboxStateFromConditions(&unstructured.Unstructured{Object: map[string]any{
		"status": map[string]any{"conditions": []any{
			map[string]any{"type": "Finished", "status": "True", "reason": "Completed", "message": "done"},
		}},
	}})
	if finished.Status != "error" || finished.ErrorMessage == nil || *finished.ErrorMessage != "done" {
		t.Fatalf("finished state = %#v, want terminal error with message", finished)
	}
}

func TestSandboxBackendStatusRecreatesPodFailedSandbox(t *testing.T) {
	instance := sandboxTestInstance(90, "running")
	conditions := []any{
		map[string]any{"type": "Ready", "status": "False", "reason": "PodFailed", "observedGeneration": int64(1)},
		map[string]any{"type": "Finished", "status": "True", "reason": "PodFailed", "observedGeneration": int64(1)},
	}
	sandbox := sandboxObjectForTest(instance, conditions)
	instanceRepo := newV2LifecycleInstanceRepo()
	instanceRepo.byID[instance.ID] = instance
	dynamicClient := newDynamicClientWithSandboxes(t, sandbox)
	backend := newSandboxBackendForTest(instanceRepo, dynamicClient)
	backend.deletePoll = time.Nanosecond
	backend.deleteTimeout = time.Second

	status, err := backend.Status(context.Background(), instance)
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if status.Status != "creating" {
		t.Fatalf("status after PodFailed recreate = %q, want creating", status.Status)
	}
	assertAction(t, dynamicClient, "delete")
	assertAction(t, dynamicClient, "create")
	recreated, err := dynamicClient.Resource(sandboxGVR).Namespace("clawreef-user-45").Get(context.Background(), "clawreef-90-isolated", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get recreated sandbox: %v", err)
	}
	if _, ok, _ := unstructured.NestedMap(recreated.Object, "status"); ok {
		t.Fatalf("recreated sandbox must not carry stale status: %#v", recreated.Object["status"])
	}
	if mode, _, _ := unstructured.NestedString(recreated.Object, "spec", "operatingMode"); mode != sandboxOperatingModeRunning {
		t.Fatalf("recreated operatingMode = %q, want Running", mode)
	}
}

func TestDefaultEgressProxyPrecheckParsesAndDialsProxy(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			_ = conn.Close()
		}
	}()

	if err := defaultEgressProxyPrecheck(context.Background(), "http://"+listener.Addr().String()); err != nil {
		t.Fatalf("defaultEgressProxyPrecheck returned error: %v", err)
	}
	<-done
	if _, err := egressProxyDialAddress("not a url"); err == nil {
		t.Fatalf("expected invalid proxy URL to fail parsing")
	}
}

func newSandboxBackendForTest(instanceRepo *v2LifecycleInstanceRepo, dynamicClient dynamic.Interface) *sandboxBackend {
	if dynamicClient == nil {
		dynamicClient = dynamicfake.NewSimpleDynamicClient(kruntime.NewScheme())
	}
	return &sandboxBackend{
		service: &instanceService{
			instanceRepo: instanceRepo,
			llmModelRepo: &stubLLMModelRepository{active: []models.LLMModel{{
				DisplayName:       "auto",
				ProviderType:      "openai-compatible",
				ProviderModelName: "auto",
				IsActive:          true,
			}}},
		},
		capabilities:  isolatedAvailableCapabilities(),
		k8sClient:     &k8s.Client{Namespace: "clawreef"},
		dynamicClient: dynamicClient,
		proxyURL: func() (string, bool) {
			return "http://proxy.good:3128", true
		},
		proxyPrecheck: func(context.Context, string) error { return nil },
		deletePoll:    time.Nanosecond,
		deleteTimeout: time.Second,
	}
}

func newDynamicClientWithSandboxes(t *testing.T, sandboxes ...*unstructured.Unstructured) *dynamicfake.FakeDynamicClient {
	t.Helper()
	client := dynamicfake.NewSimpleDynamicClient(kruntime.NewScheme())
	for _, sandbox := range sandboxes {
		if _, err := client.Resource(sandboxGVR).Namespace(sandbox.GetNamespace()).Create(context.Background(), sandbox, metav1.CreateOptions{}); err != nil {
			t.Fatalf("seed sandbox %s/%s: %v", sandbox.GetNamespace(), sandbox.GetName(), err)
		}
	}
	client.ClearActions()
	return client
}

func sandboxTestInstance(id int, status string) *models.Instance {
	namespace := "clawreef-user-45"
	name := "clawreef-" + strconv.Itoa(id) + "-isolated"
	accessToken := "igt_test"
	agentToken := "agt_boot_test"
	workspacePath := "/workspaces/openclaw/user-45/instance-" + strconv.Itoa(id)
	return &models.Instance{
		ID:                  id,
		UserID:              45,
		Name:                "Isolated",
		Type:                RuntimeTypeOpenClaw,
		RuntimeType:         RuntimeBackendGateway,
		InstanceMode:        InstanceModeIsolated,
		Status:              status,
		CPUCores:            2,
		MemoryGB:            4,
		DiskGB:              20,
		PodNamespace:        &namespace,
		PodName:             &name,
		AccessToken:         &accessToken,
		AgentBootstrapToken: &agentToken,
		WorkspacePath:       &workspacePath,
		RuntimeGeneration:   1,
		CreatedAt:           time.Now(),
	}
}

func sandboxObjectForTest(instance *models.Instance, conditions []any) *unstructured.Unstructured {
	namespace := "clawreef-user-45"
	if instance.PodNamespace != nil {
		namespace = *instance.PodNamespace
	}
	name := "clawreef-" + strconv.Itoa(instance.ID) + "-isolated"
	if instance.PodName != nil {
		name = *instance.PodName
	}
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": sandboxAPIVersion,
		"kind":       sandboxKind,
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
		},
		"spec": map[string]any{
			"operatingMode": sandboxOperatingModeRunning,
			"podTemplate": map[string]any{
				"metadata": map[string]any{"annotations": stringMapToAny(isolatedPrometheusAnnotations())},
				"spec": map[string]any{
					"containers": []any{map[string]any{"name": RuntimeTypeOpenClaw, "image": "registry/openclaw-lite:latest"}},
				},
			},
		},
	}}
	if conditions != nil {
		obj.Object["status"] = map[string]any{
			"conditions": conditions,
			"podIPs":     []any{"10.0.0.9"},
		}
	}
	return obj
}

func nestedSlice(t *testing.T, obj *unstructured.Unstructured, fields ...string) []interface{} {
	t.Helper()
	items, ok, err := unstructured.NestedSlice(obj.Object, fields...)
	if err != nil || !ok {
		t.Fatalf("NestedSlice(%v) = %#v/%v/%v", fields, items, ok, err)
	}
	return items
}

func envMapFromContainer(t *testing.T, container map[string]interface{}) map[string]string {
	t.Helper()
	envItems := container["env"].([]interface{})
	env := map[string]string{}
	for _, item := range envItems {
		entry := item.(map[string]interface{})
		env[entry["name"].(string)] = entry["value"].(string)
	}
	return env
}

func stringSliceFromInterface(value interface{}) []string {
	items := value.([]interface{})
	result := make([]string, 0, len(items))
	for _, item := range items {
		result = append(result, item.(string))
	}
	return result
}

func assertAction(t *testing.T, client *dynamicfake.FakeDynamicClient, verb string) {
	t.Helper()
	for _, action := range client.Actions() {
		if action.GetVerb() == verb && action.GetResource().Resource == "sandboxes" {
			return
		}
	}
	var verbs []string
	for _, action := range client.Actions() {
		if resourceAction, ok := action.(k8stesting.Action); ok {
			verbs = append(verbs, resourceAction.GetVerb()+"/"+resourceAction.GetResource().Resource)
		}
	}
	t.Fatalf("expected %s action for sandboxes, got %v", verb, verbs)
}

func mustMarshalEnvOverrides(t *testing.T, env map[string]string) *string {
	t.Helper()
	raw, err := marshalEnvironmentOverrides(env)
	if err != nil {
		t.Fatalf("marshal env overrides: %v", err)
	}
	return raw
}
