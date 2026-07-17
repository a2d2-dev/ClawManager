package services

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"clawreef/internal/models"
	"clawreef/internal/services/k8s"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/dynamic"
)

const (
	agentSandboxVersionNote = "agent-sandbox v0.5.1"
	sandboxAPIVersion       = "agents.x-k8s.io/v1beta1"
	sandboxKind             = "Sandbox"

	sandboxOperatingModeRunning   = "Running"
	sandboxOperatingModeSuspended = "Suspended"

	isolatedGatewayPort     int32 = 19090
	isolatedAgentPublicPort       = "19090"

	isolatedWorkspaceMountPath = "/workspaces"
	isolatedConfigMountPath    = "/config"

	sandboxRecreateAttemptsAnnotation    = "clawmanager.io/recreate-attempts"
	sandboxRecreateLastAttemptAnnotation = "clawmanager.io/recreate-last-attempt"
	sandboxRecreateMaxAttempts           = 3
	sandboxRecreateCooldown              = 5 * time.Minute
)

// agent-sandbox is pinned to v0.5.1/v1beta1. Re-check this GVR and the
// condition semantics before upgrading to v0.5.2+.
var sandboxGVR = schema.GroupVersionResource{
	Group:    AgentSandboxGroup,
	Version:  "v1beta1",
	Resource: "sandboxes",
}

type sandboxBackend struct {
	service       *instanceService
	capabilities  RuntimeCapabilities
	k8sClient     *k8s.Client
	dynamicClient dynamic.Interface
	initErr       error
	proxyURL      func() (string, bool)
	proxyPrecheck egressProxyPrecheck
	deletePoll    time.Duration
	deleteTimeout time.Duration
}

type isolatedSandboxSpec struct {
	Instance       *models.Instance
	Name           string
	Namespace      string
	Image          string
	RuntimeEnv     map[string]string
	GatewayEnv     map[string]string
	AgentEnv       map[string]string
	ProxyURL       string
	StorageClass   string
	Placement      *RuntimePlacement
	EnvFromSecrets []string
}

type sandboxConditionState struct {
	Status       string
	PodStatus    string
	Reason       string
	Recreate     bool
	ErrorMessage *string
}

type sandboxCondition struct {
	Type               string
	Status             string
	Reason             string
	Message            string
	LastTransitionTime time.Time
	ObservedGeneration int64
	Index              int
}

type sandboxRecoveryDecision struct {
	Status       string
	PodStatus    string
	ErrorMessage *string
}

func newSandboxBackend(s *instanceService) *sandboxBackend {
	backend := &sandboxBackend{
		service:       s,
		capabilities:  defaultRuntimeCapabilities(),
		proxyURL:      defaultEgressProxyURL,
		proxyPrecheck: defaultEgressProxyPrecheck,
		deletePoll:    500 * time.Millisecond,
		deleteTimeout: 30 * time.Second,
	}
	if s != nil {
		backend.capabilities = normalizeRuntimeCapabilities(s.runtimeCapabilities)
	}
	if client := k8s.GetClient(); client != nil {
		backend.k8sClient = client
		if client.Config != nil {
			dynamicClient, err := dynamic.NewForConfig(client.Config)
			if err != nil {
				backend.initErr = fmt.Errorf("mode unavailable: failed to initialize agent-sandbox dynamic client: %w", err)
			} else {
				backend.dynamicClient = dynamicClient
			}
		}
	}
	return backend
}

func (b *sandboxBackend) Create(ctx context.Context, userID int, req CreateInstanceRequest, instanceMode string, runtimeType string, environmentOverridesJSON *string) (*models.Instance, error) {
	s := b.service
	if s == nil || s.instanceRepo == nil {
		return nil, fmt.Errorf("isolated runtime backend is not configured")
	}
	if instanceMode != InstanceModeIsolated {
		return nil, fmt.Errorf("isolated runtime backend cannot create %s instances", instanceMode)
	}
	if runtimeType != RuntimeBackendGateway {
		return nil, fmt.Errorf("isolated instance mode requires runtime_type=gateway")
	}
	managedRuntimeType, ok := NormalizeV2RuntimeType(req.Type)
	if !ok {
		return nil, fmt.Errorf("isolated runtime backend requires managed runtime type")
	}
	if err := rejectIsolatedCustomImage(req.ImageRegistry, req.ImageTag); err != nil {
		return nil, err
	}
	if err := b.ensureAvailable(); err != nil {
		return nil, err
	}
	if overrides, err := parseEnvironmentOverridesJSON(environmentOverridesJSON); err != nil {
		return nil, err
	} else if err := rejectIsolatedReservedProxyOverrides(overrides); err != nil {
		return nil, err
	}
	proxyURL, err := b.requireProxyURL()
	if err != nil {
		return nil, err
	}
	if err := b.proxyPrecheck(ctx, proxyURL); err != nil {
		return nil, err
	}

	now := time.Now()
	instance := &models.Instance{
		UserID:                   userID,
		Name:                     strings.TrimSpace(req.Name),
		Description:              trimOptionalString(req.Description),
		Type:                     managedRuntimeType,
		RuntimeType:              RuntimeBackendGateway,
		InstanceMode:             InstanceModeIsolated,
		Status:                   "creating",
		CPUCores:                 req.CPUCores,
		MemoryGB:                 req.MemoryGB,
		DiskGB:                   req.DiskGB,
		GPUEnabled:               req.GPUEnabled,
		GPUCount:                 req.GPUCount,
		OSType:                   req.OSType,
		OSVersion:                req.OSVersion,
		EnvironmentOverridesJSON: environmentOverridesJSON,
		StorageClass:             strings.TrimSpace(req.StorageClass),
		MountPath:                isolatedWorkspaceMountPath,
		RuntimeGeneration:        1,
		CreatedAt:                now,
		UpdatedAt:                now,
		StartedAt:                &now,
	}

	if err := s.instanceRepo.Create(instance); err != nil {
		return nil, fmt.Errorf("failed to create isolated instance record: %w", err)
	}
	cleanup := true
	var bootstrapSnapshot *models.OpenClawInjectionSnapshot
	defer func() {
		if cleanup {
			deleteErr := b.deleteSandboxObject(context.Background(), instance)
			if bootstrapSnapshot != nil && s.openClawConfigService != nil {
				_ = s.openClawConfigService.MarkSnapshotFailed(bootstrapSnapshot, fmt.Errorf("isolated create rolled back"))
			}
			if deleteErr != nil {
				message := fmt.Sprintf("isolated create rollback failed to delete Sandbox; preserving instance record: %v", deleteErr)
				instance.Status = "error"
				instance.RuntimeErrorMessage = &message
				instance.UpdatedAt = time.Now()
				if updateErr := s.instanceRepo.Update(instance); updateErr != nil {
					log.Printf("isolated create rollback failed to mark instance %d error after Sandbox delete failure: %v", instance.ID, updateErr)
				}
				log.Printf("isolated create rollback preserved instance %d because Sandbox delete failed: %v", instance.ID, deleteErr)
				return
			}
			_ = s.instanceRepo.Delete(instance.ID)
		}
	}()

	workspacePath := RuntimeWorkspacePath(managedRuntimeType, userID, instance.ID)
	if err := s.instanceRepo.SetWorkspacePath(ctx, instance.ID, workspacePath); err != nil {
		return nil, fmt.Errorf("failed to persist isolated workspace path: %w", err)
	}
	instance.WorkspacePath = &workspacePath

	if _, err := s.ensureGatewayToken(instance); err != nil {
		return nil, fmt.Errorf("failed to provision isolated gateway token: %w", err)
	}
	if _, err := s.ensureAgentBootstrapToken(instance); err != nil {
		return nil, fmt.Errorf("failed to provision isolated agent bootstrap token: %w", err)
	}

	var bootstrapSecretName string
	if supportsRuntimeConfigInjection(instance.Type) && s.openClawConfigService != nil && req.OpenClawConfigPlan != nil && hasOpenClawConfigSelections(*req.OpenClawConfigPlan) {
		snapshot, err := s.openClawConfigService.CreateSnapshotForInstance(userID, instance, req.OpenClawConfigPlan)
		if err != nil {
			return nil, fmt.Errorf("failed to compile isolated runtime bootstrap config: %w", err)
		}
		bootstrapSnapshot = snapshot
		if bootstrapSnapshot != nil {
			instance.OpenClawConfigSnapshotID = &bootstrapSnapshot.ID
			instance.UpdatedAt = time.Now()
			if err := s.instanceRepo.Update(instance); err != nil {
				return nil, fmt.Errorf("failed to persist isolated runtime snapshot reference: %w", err)
			}
			bootstrapSecretName, err = s.openClawConfigService.EnsureSnapshotSecret(ctx, userID, instance, bootstrapSnapshot.ID)
			if err != nil {
				_ = s.openClawConfigService.MarkSnapshotFailed(bootstrapSnapshot, err)
				return nil, fmt.Errorf("failed to provision isolated runtime bootstrap secret: %w", err)
			}
		}
	}

	gatewayEnv, agentEnv, err := b.governedEnv(instance)
	if err != nil {
		return nil, err
	}
	image := isolatedGatewayImage(instance.Type)
	sandbox, err := buildIsolatedSandboxObject(isolatedSandboxSpec{
		Instance:       instance,
		Name:           b.sandboxName(instance),
		Namespace:      b.namespace(userID),
		Image:          image,
		RuntimeEnv:     isolatedBaseRuntimeEnv(instance),
		GatewayEnv:     gatewayEnv,
		AgentEnv:       agentEnv,
		ProxyURL:       proxyURL,
		StorageClass:   instance.StorageClass,
		Placement:      req.Placement,
		EnvFromSecrets: []string{bootstrapSecretName},
	})
	if err != nil {
		return nil, err
	}
	if err := b.ensureNamespace(ctx, userID); err != nil {
		return nil, err
	}
	created, err := b.sandboxes(sandbox.GetNamespace()).Create(ctx, sandbox, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		created, err = b.sandboxes(sandbox.GetNamespace()).Update(ctx, sandbox, metav1.UpdateOptions{})
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create isolated Sandbox: %w", err)
	}

	namespace := created.GetNamespace()
	name := created.GetName()
	instance.PodNamespace = &namespace
	instance.PodName = &name
	instance.Status = "creating"
	instance.UpdatedAt = time.Now()
	if err := s.instanceRepo.Update(instance); err != nil {
		return nil, fmt.Errorf("failed to update isolated instance workload info: %w", err)
	}
	if bootstrapSnapshot != nil {
		if err := s.openClawConfigService.MarkSnapshotActive(bootstrapSnapshot); err != nil {
			return nil, fmt.Errorf("failed to activate isolated runtime bootstrap snapshot: %w", err)
		}
	}

	cleanup = false
	GetHub().BroadcastInstanceStatus(userID, instance)
	return instance, nil
}

func (b *sandboxBackend) Start(ctx context.Context, instance *models.Instance, runtimeType string) error {
	if runtimeType != RuntimeBackendGateway {
		return fmt.Errorf("isolated instance mode requires runtime_type=gateway")
	}
	if err := b.ensureAvailable(); err != nil {
		return err
	}
	proxyURL, err := b.requireProxyURL()
	if err != nil {
		return err
	}
	if err := b.proxyPrecheck(ctx, proxyURL); err != nil {
		return err
	}
	sandbox, err := b.getSandbox(ctx, instance)
	if apierrors.IsNotFound(err) {
		if err := b.recreateFromInstance(ctx, instance, proxyURL); err != nil {
			return err
		}
		return b.markInstanceCreating(instance)
	}
	if err != nil {
		return err
	}
	if err := b.refreshSandboxPodEnv(sandbox, instance, proxyURL); err != nil {
		return err
	}
	if err := unstructured.SetNestedField(sandbox.Object, sandboxOperatingModeRunning, "spec", "operatingMode"); err != nil {
		return fmt.Errorf("failed to set isolated Sandbox running mode: %w", err)
	}
	if _, err := b.sandboxes(sandbox.GetNamespace()).Update(ctx, sandbox, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to start isolated Sandbox: %w", err)
	}
	return b.markInstanceCreating(instance)
}

func (b *sandboxBackend) Stop(ctx context.Context, instance *models.Instance) error {
	if err := b.ensureAvailable(); err != nil {
		return err
	}
	sandbox, err := b.getSandbox(ctx, instance)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return b.markInstanceStopped(instance)
		}
		return err
	}
	if err := unstructured.SetNestedField(sandbox.Object, sandboxOperatingModeSuspended, "spec", "operatingMode"); err != nil {
		return fmt.Errorf("failed to set isolated Sandbox suspended mode: %w", err)
	}
	if _, err := b.sandboxes(sandbox.GetNamespace()).Update(ctx, sandbox, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to stop isolated Sandbox: %w", err)
	}
	return b.markInstanceStopped(instance)
}

func (b *sandboxBackend) Delete(ctx context.Context, instance *models.Instance) error {
	if err := b.ensureAvailable(); err != nil {
		return err
	}
	s := b.service
	if s == nil || s.instanceRepo == nil {
		return fmt.Errorf("isolated runtime backend is not configured")
	}
	if instance.Status != "deleting" {
		now := time.Now()
		instance.Status = "deleting"
		instance.UpdatedAt = now
		if err := s.instanceRepo.Update(instance); err != nil {
			return fmt.Errorf("failed to mark isolated instance as deleting: %w", err)
		}
		GetHub().BroadcastInstanceStatus(instance.UserID, instance)
	}
	if err := b.deleteSandboxObject(ctx, instance); err != nil {
		return err
	}
	if err := s.instanceRepo.Delete(instance.ID); err != nil {
		return fmt.Errorf("failed to delete isolated instance record: %w", err)
	}
	return nil
}

func (b *sandboxBackend) Status(ctx context.Context, instance *models.Instance) (*InstanceStatus, error) {
	if err := b.ensureAvailable(); err != nil {
		return nil, err
	}
	sandbox, err := b.getSandbox(ctx, instance)
	if apierrors.IsNotFound(err) {
		if syncErr := b.markInstanceStopped(instance); syncErr != nil {
			return nil, syncErr
		}
		stopped := "stopped"
		return &InstanceStatus{
			InstanceID:   instance.ID,
			Status:       stopped,
			CreatedAt:    instance.CreatedAt,
			StartedAt:    instance.StartedAt,
			PodName:      instance.PodName,
			PodNamespace: instance.PodNamespace,
		}, nil
	}
	if err != nil {
		return nil, err
	}
	state := sandboxStateFromConditions(sandbox)
	if state.Recreate {
		decision, err := b.recoverFailedSandbox(ctx, sandbox, instance)
		if err != nil {
			return nil, err
		}
		state.Status = decision.Status
		state.PodStatus = decision.PodStatus
		state.Recreate = false
		state.ErrorMessage = decision.ErrorMessage
	}
	if err := b.syncInstanceStatus(instance, state); err != nil {
		return nil, err
	}

	podIP := firstSandboxPodIP(sandbox)
	return &InstanceStatus{
		InstanceID:   instance.ID,
		Status:       state.Status,
		PodName:      optionalString(sandbox.GetName()),
		PodNamespace: optionalString(sandbox.GetNamespace()),
		PodIP:        optionalString(podIP),
		PodStatus:    state.PodStatus,
		CreatedAt:    instance.CreatedAt,
		StartedAt:    instance.StartedAt,
	}, nil
}

func (b *sandboxBackend) Endpoint(ctx context.Context, instance *models.Instance) (*RuntimeEndpoint, error) {
	if err := b.ensureAvailable(); err != nil {
		return nil, err
	}
	sandbox, err := b.getSandbox(ctx, instance)
	if err != nil {
		return nil, err
	}
	name := sandbox.GetName()
	namespace := sandbox.GetNamespace()
	fqdn, _, _ := unstructured.NestedString(sandbox.Object, "status", "serviceFQDN")
	if strings.TrimSpace(fqdn) == "" {
		fqdn = fmt.Sprintf("%s.%s.svc.cluster.local", name, namespace)
	}
	return &RuntimeEndpoint{
		AgentEndpoint: fmt.Sprintf("http://%s:%d", fqdn, isolatedGatewayPort),
		PodIP:         firstSandboxPodIP(sandbox),
		Port:          int(isolatedGatewayPort),
	}, nil
}

func (b *sandboxBackend) AttachPolicy(ctx context.Context, instance *models.Instance, policy RuntimePolicyAttachment) error {
	return nil
}

func (b *sandboxBackend) Suspend(ctx context.Context, instance *models.Instance) error {
	return b.Stop(ctx, instance)
}

func (b *sandboxBackend) ensureAvailable() error {
	if b.initErr != nil {
		return b.initErr
	}
	capability := normalizeRuntimeCapabilities(b.capabilities).CapabilityForMode(InstanceModeIsolated)
	if !capability.Available {
		reason := strings.TrimSpace(capability.Reason)
		if reason == "" {
			reason = "mode unavailable: isolated runtime requires agent-sandbox Sandbox CRD"
		}
		if !strings.Contains(strings.ToLower(reason), "mode unavailable") {
			reason = "mode unavailable: " + reason
		}
		return fmt.Errorf("%s", reason)
	}
	if b.dynamicClient == nil {
		return fmt.Errorf("mode unavailable: agent-sandbox Sandbox dynamic client is not configured")
	}
	if b.proxyPrecheck == nil {
		b.proxyPrecheck = defaultEgressProxyPrecheck
	}
	if b.proxyURL == nil {
		b.proxyURL = defaultEgressProxyURL
	}
	return nil
}

func (b *sandboxBackend) requireProxyURL() (string, error) {
	proxyURL, ok := b.proxyURL()
	if !ok || strings.TrimSpace(proxyURL) == "" {
		return "", fmt.Errorf("mode unavailable: isolated runtime requires a computable egress proxy URL")
	}
	return strings.TrimSpace(proxyURL), nil
}

func (b *sandboxBackend) sandboxes(namespace string) dynamic.ResourceInterface {
	return b.dynamicClient.Resource(sandboxGVR).Namespace(namespace)
}

func (b *sandboxBackend) sandboxName(instance *models.Instance) string {
	if instance == nil {
		return "clawreef-isolated"
	}
	if instance.PodName != nil && strings.TrimSpace(*instance.PodName) != "" {
		return strings.TrimSpace(*instance.PodName)
	}
	if b.k8sClient != nil {
		return b.k8sClient.GetDeploymentName(instance.ID, instance.Name)
	}
	return fmt.Sprintf("clawreef-%d-%s", instance.ID, strings.ToLower(strings.TrimSpace(instance.Name)))
}

func (b *sandboxBackend) namespace(userID int) string {
	if b.k8sClient != nil {
		return b.k8sClient.GetNamespace(userID)
	}
	return fmt.Sprintf("clawreef-user-%d", userID)
}

func (b *sandboxBackend) getSandbox(ctx context.Context, instance *models.Instance) (*unstructured.Unstructured, error) {
	namespace := b.namespace(instance.UserID)
	if instance.PodNamespace != nil && strings.TrimSpace(*instance.PodNamespace) != "" {
		namespace = strings.TrimSpace(*instance.PodNamespace)
	}
	name := b.sandboxName(instance)
	sandbox, err := b.sandboxes(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, err
		}
		return nil, fmt.Errorf("failed to get isolated Sandbox %s/%s: %w", namespace, name, err)
	}
	return sandbox, nil
}

func (b *sandboxBackend) ensureNamespace(ctx context.Context, userID int) error {
	if b.k8sClient == nil || b.k8sClient.Clientset == nil {
		return nil
	}
	namespace := b.namespace(userID)
	_, err := b.k8sClient.Clientset.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get namespace %s: %w", namespace, err)
	}
	_, err = b.k8sClient.Clientset.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: namespace},
	}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create namespace %s: %w", namespace, err)
	}
	return nil
}

func (b *sandboxBackend) governedEnv(instance *models.Instance) (map[string]string, map[string]string, error) {
	if b.service == nil {
		return nil, nil, fmt.Errorf("isolated runtime backend is not configured")
	}
	gatewayEnv, err := b.service.buildGatewayEnv(instance)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build isolated gateway config: %w", err)
	}
	agentEnv, err := b.service.buildAgentEnv(instance)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build isolated agent config: %w", err)
	}
	return gatewayEnv, agentEnv, nil
}

func (b *sandboxBackend) refreshSandboxPodEnv(sandbox *unstructured.Unstructured, instance *models.Instance, proxyURL string) error {
	gatewayEnv, agentEnv, err := b.governedEnv(instance)
	if err != nil {
		return err
	}
	env, err := buildIsolatedSandboxEnv(instance, isolatedBaseRuntimeEnv(instance), gatewayEnv, agentEnv, proxyURL)
	if err != nil {
		return err
	}
	renderedEnv, err := envVarsToUnstructured(env)
	if err != nil {
		return err
	}

	containers, ok, err := unstructured.NestedSlice(sandbox.Object, "spec", "podTemplate", "spec", "containers")
	if err != nil {
		return fmt.Errorf("failed to inspect isolated Sandbox containers: %w", err)
	}
	if !ok || len(containers) == 0 {
		return fmt.Errorf("isolated Sandbox pod template has no containers")
	}

	targetIndex := 0
	containerName := normalizedGatewayContainerName(instance.Type)
	for idx, item := range containers {
		container, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(stringField(container, "name")), containerName) {
			targetIndex = idx
			break
		}
	}
	container, ok := containers[targetIndex].(map[string]any)
	if !ok {
		return fmt.Errorf("isolated Sandbox pod template container is invalid")
	}
	container["env"] = renderedEnv
	containers[targetIndex] = container
	if err := unstructured.SetNestedSlice(sandbox.Object, containers, "spec", "podTemplate", "spec", "containers"); err != nil {
		return fmt.Errorf("failed to refresh isolated Sandbox pod env: %w", err)
	}
	return nil
}

func (b *sandboxBackend) recreateFromInstance(ctx context.Context, instance *models.Instance, proxyURL string) error {
	if b.service == nil {
		return fmt.Errorf("isolated runtime backend is not configured")
	}
	if _, err := b.service.ensureGatewayToken(instance); err != nil {
		return fmt.Errorf("failed to provision isolated gateway token: %w", err)
	}
	if _, err := b.service.ensureAgentBootstrapToken(instance); err != nil {
		return fmt.Errorf("failed to provision isolated agent bootstrap token: %w", err)
	}
	gatewayEnv, agentEnv, err := b.governedEnv(instance)
	if err != nil {
		return err
	}
	var envFromSecrets []string
	if supportsRuntimeConfigInjection(instance.Type) && b.service.openClawConfigService != nil && instance.OpenClawConfigSnapshotID != nil && *instance.OpenClawConfigSnapshotID > 0 {
		secretName, err := b.service.openClawConfigService.EnsureSnapshotSecret(ctx, instance.UserID, instance, *instance.OpenClawConfigSnapshotID)
		if err != nil {
			return fmt.Errorf("failed to restore isolated runtime bootstrap secret: %w", err)
		}
		envFromSecrets = []string{secretName}
	}
	sandbox, err := buildIsolatedSandboxObject(isolatedSandboxSpec{
		Instance:       instance,
		Name:           b.sandboxName(instance),
		Namespace:      b.namespace(instance.UserID),
		Image:          isolatedGatewayImage(instance.Type),
		RuntimeEnv:     isolatedBaseRuntimeEnv(instance),
		GatewayEnv:     gatewayEnv,
		AgentEnv:       agentEnv,
		ProxyURL:       proxyURL,
		StorageClass:   instance.StorageClass,
		EnvFromSecrets: envFromSecrets,
	})
	if err != nil {
		return err
	}
	if err := b.ensureNamespace(ctx, instance.UserID); err != nil {
		return err
	}
	if _, err := b.sandboxes(sandbox.GetNamespace()).Create(ctx, sandbox, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to recreate isolated Sandbox: %w", err)
	}
	return nil
}

func (b *sandboxBackend) recoverFailedSandbox(ctx context.Context, existing *unstructured.Unstructured, instance *models.Instance) (sandboxRecoveryDecision, error) {
	namespace := existing.GetNamespace()
	name := existing.GetName()
	now := time.Now().UTC()
	annotations := cloneStringMap(existing.GetAnnotations())
	attempts, _ := strconv.Atoi(strings.TrimSpace(annotations[sandboxRecreateAttemptsAnnotation]))
	lastAttempt := parseSandboxRecreateAttemptTime(annotations[sandboxRecreateLastAttemptAnnotation])

	if attempts >= sandboxRecreateMaxAttempts {
		message := fmt.Sprintf("isolated Sandbox recovery stopped after %d failed recreate attempts", attempts)
		return sandboxRecoveryDecision{Status: "error", PodStatus: "recreate_limit_exceeded", ErrorMessage: &message}, nil
	}
	if attempts > 0 && !lastAttempt.IsZero() && now.Sub(lastAttempt) < sandboxRecreateCooldown {
		return sandboxRecoveryDecision{Status: "creating", PodStatus: "recreate_cooldown"}, nil
	}

	proxyURL, err := b.requireProxyURL()
	if err != nil {
		return sandboxRecoveryDecision{}, err
	}
	suspended := existing.DeepCopy()
	if err := b.refreshSandboxPodEnv(suspended, instance, proxyURL); err != nil {
		return sandboxRecoveryDecision{}, err
	}
	annotations = cloneStringMap(suspended.GetAnnotations())
	annotations[sandboxRecreateAttemptsAnnotation] = strconv.Itoa(attempts + 1)
	annotations[sandboxRecreateLastAttemptAnnotation] = now.Format(time.RFC3339)
	suspended.SetAnnotations(annotations)
	if err := unstructured.SetNestedField(suspended.Object, sandboxOperatingModeSuspended, "spec", "operatingMode"); err != nil {
		return sandboxRecoveryDecision{}, fmt.Errorf("failed to set failed isolated Sandbox suspended mode: %w", err)
	}
	if _, err := b.sandboxes(namespace).Update(ctx, suspended, metav1.UpdateOptions{}); err != nil {
		return sandboxRecoveryDecision{}, fmt.Errorf("failed to suspend failed isolated Sandbox %s/%s before recovery: %w", namespace, name, err)
	}
	if err := b.waitSandboxPodDeleted(ctx, namespace, name); err != nil {
		return sandboxRecoveryDecision{}, err
	}

	running, err := b.sandboxes(namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return sandboxRecoveryDecision{}, fmt.Errorf("failed to recover isolated Sandbox %s/%s: Sandbox disappeared while suspended", namespace, name)
	}
	if err != nil {
		return sandboxRecoveryDecision{}, fmt.Errorf("failed to reload isolated Sandbox %s/%s for recovery: %w", namespace, name, err)
	}
	if err := b.refreshSandboxPodEnv(running, instance, proxyURL); err != nil {
		return sandboxRecoveryDecision{}, err
	}
	running.SetAnnotations(annotations)
	if err := unstructured.SetNestedField(running.Object, sandboxOperatingModeRunning, "spec", "operatingMode"); err != nil {
		return sandboxRecoveryDecision{}, fmt.Errorf("failed to set failed isolated Sandbox running mode: %w", err)
	}
	if _, err := b.sandboxes(namespace).Update(ctx, running, metav1.UpdateOptions{}); err != nil {
		return sandboxRecoveryDecision{}, fmt.Errorf("failed to resume failed isolated Sandbox %s/%s: %w", namespace, name, err)
	}
	return sandboxRecoveryDecision{Status: "creating", PodStatus: "recreating"}, nil
}

func parseSandboxRecreateAttemptTime(raw string) time.Time {
	parsed, _ := time.Parse(time.RFC3339, strings.TrimSpace(raw))
	return parsed
}

func (b *sandboxBackend) waitSandboxPodDeleted(ctx context.Context, namespace, name string) error {
	if b.k8sClient == nil || b.k8sClient.Clientset == nil {
		return nil
	}
	timeout := b.deleteTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	poll := b.deletePoll
	if poll <= 0 {
		poll = 500 * time.Millisecond
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		_, err := b.k8sClient.Clientset.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("failed waiting for isolated Sandbox pod deletion %s/%s: %w", namespace, name, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("timed out waiting for isolated Sandbox pod %s/%s deletion", namespace, name)
		case <-ticker.C:
		}
	}
}

func (b *sandboxBackend) deleteSandboxObject(ctx context.Context, instance *models.Instance) error {
	if b.dynamicClient == nil || instance == nil {
		return nil
	}
	namespace := b.namespace(instance.UserID)
	if instance.PodNamespace != nil && strings.TrimSpace(*instance.PodNamespace) != "" {
		namespace = strings.TrimSpace(*instance.PodNamespace)
	}
	name := b.sandboxName(instance)
	err := b.sandboxes(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete isolated Sandbox %s/%s: %w", namespace, name, err)
	}
	return nil
}

func (b *sandboxBackend) markInstanceCreating(instance *models.Instance) error {
	if b.service == nil || b.service.instanceRepo == nil {
		return nil
	}
	nextGeneration := instance.RuntimeGeneration + 1
	if nextGeneration <= 0 {
		nextGeneration = 1
	}
	now := time.Now()
	instance.Status = "creating"
	instance.RuntimeGeneration = nextGeneration
	instance.RuntimeErrorMessage = nil
	instance.StartedAt = &now
	instance.UpdatedAt = now
	namespace := b.namespace(instance.UserID)
	name := b.sandboxName(instance)
	instance.PodNamespace = &namespace
	instance.PodName = &name
	if err := b.service.instanceRepo.UpdateRuntimeState(context.Background(), instance.ID, "creating", nextGeneration, nil); err != nil {
		return fmt.Errorf("failed to mark isolated instance creating: %w", err)
	}
	if err := b.service.instanceRepo.Update(instance); err != nil {
		return fmt.Errorf("failed to update isolated instance workload info: %w", err)
	}
	GetHub().BroadcastInstanceStatus(instance.UserID, instance)
	return nil
}

func (b *sandboxBackend) markInstanceStopped(instance *models.Instance) error {
	if b.service == nil || b.service.instanceRepo == nil {
		return nil
	}
	now := time.Now()
	instance.Status = "stopped"
	instance.StoppedAt = &now
	instance.PodIP = nil
	instance.RuntimeErrorMessage = nil
	instance.UpdatedAt = now
	if err := b.service.instanceRepo.Update(instance); err != nil {
		return fmt.Errorf("failed to update isolated instance status: %w", err)
	}
	GetHub().BroadcastInstanceStatus(instance.UserID, instance)
	return nil
}

func (b *sandboxBackend) syncInstanceStatus(instance *models.Instance, state sandboxConditionState) error {
	if b.service == nil || b.service.instanceRepo == nil || instance == nil || state.Status == "" {
		return nil
	}
	needsUpdate := instance.Status != state.Status
	if state.ErrorMessage != nil {
		if instance.RuntimeErrorMessage == nil || *instance.RuntimeErrorMessage != *state.ErrorMessage {
			needsUpdate = true
		}
	} else if instance.RuntimeErrorMessage != nil {
		needsUpdate = true
	}
	if !needsUpdate {
		return nil
	}
	instance.Status = state.Status
	instance.RuntimeErrorMessage = state.ErrorMessage
	instance.UpdatedAt = time.Now()
	if err := b.service.instanceRepo.Update(instance); err != nil {
		return fmt.Errorf("failed to sync isolated instance status: %w", err)
	}
	GetHub().BroadcastInstanceStatus(instance.UserID, instance)
	return nil
}

func buildIsolatedSandboxObject(spec isolatedSandboxSpec) (*unstructured.Unstructured, error) {
	if spec.Instance == nil {
		return nil, fmt.Errorf("instance is required")
	}
	instance := spec.Instance
	namespace := firstNonEmptyString(spec.Namespace, fmt.Sprintf("clawreef-user-%d", instance.UserID))
	name := firstNonEmptyString(spec.Name, fmt.Sprintf("clawreef-%d-%s", instance.ID, strings.ToLower(strings.TrimSpace(instance.Name))))
	labels := isolatedSandboxLabels(instance, name)
	env, err := buildIsolatedSandboxEnv(instance, spec.RuntimeEnv, spec.GatewayEnv, spec.AgentEnv, spec.ProxyURL)
	if err != nil {
		return nil, err
	}
	placement := RuntimePlacement{}
	if spec.Placement != nil {
		placement = *spec.Placement
	}
	container := corev1.Container{
		Name:            normalizedGatewayContainerName(instance.Type),
		Image:           spec.Image,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Command:         isolatedGatewayCommand(instance.Type),
		Env:             env,
		Ports: []corev1.ContainerPort{{
			Name:          "gateway",
			ContainerPort: isolatedGatewayPort,
			Protocol:      corev1.ProtocolTCP,
		}},
		Resources: isolatedResourceRequirements(instance),
		VolumeMounts: []corev1.VolumeMount{
			{Name: "workspace", MountPath: isolatedWorkspaceMountPath},
			{Name: "config", MountPath: isolatedConfigMountPath},
		},
		StartupProbe: &corev1.Probe{
			ProbeHandler:     corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(isolatedGatewayPort)}},
			FailureThreshold: 30,
			PeriodSeconds:    5,
			TimeoutSeconds:   2,
		},
	}
	for _, secretName := range spec.EnvFromSecrets {
		if strings.TrimSpace(secretName) == "" {
			continue
		}
		container.EnvFrom = append(container.EnvFrom, corev1.EnvFromSource{
			SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: strings.TrimSpace(secretName)}},
		})
	}

	automount := false
	runtimeClassName := strings.TrimSpace(placement.RuntimeClassName)
	podSpec := corev1.PodSpec{
		AutomountServiceAccountToken: &automount,
		RestartPolicy:                corev1.RestartPolicyAlways,
		NodeSelector:                 cloneStringMap(placement.NodeSelector.MatchLabels),
		RuntimeClassName:             &runtimeClassName,
		Tolerations:                  append([]corev1.Toleration(nil), placement.Tolerations...),
		Containers:                   []corev1.Container{container},
	}
	if runtimeClassName == "" {
		podSpec.RuntimeClassName = nil
	}
	if len(placement.NodeSelector.MatchExpressions) > 0 {
		podSpec.Affinity = &corev1.Affinity{
			NodeAffinity: &corev1.NodeAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
					NodeSelectorTerms: []corev1.NodeSelectorTerm{{
						MatchExpressions: append([]corev1.NodeSelectorRequirement(nil), placement.NodeSelector.MatchExpressions...),
					}},
				},
			},
		}
	}
	podTemplate := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels:      cloneStringMap(labels),
			Annotations: isolatedPrometheusAnnotations(),
		},
		Spec: podSpec,
	}
	podTemplateMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&podTemplate)
	if err != nil {
		return nil, fmt.Errorf("failed to render isolated Sandbox pod template: %w", err)
	}
	volumeTemplates, err := isolatedVolumeClaimTemplates(instance, spec.StorageClass)
	if err != nil {
		return nil, err
	}
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": sandboxAPIVersion,
		"kind":       sandboxKind,
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
			"labels":    stringMapToAny(labels),
			"annotations": map[string]any{
				"clawmanager.io/agent-sandbox-version": agentSandboxVersionNote,
			},
		},
		"spec": map[string]any{
			"service":              true,
			"operatingMode":        sandboxOperatingModeRunning,
			"podTemplate":          podTemplateMap,
			"volumeClaimTemplates": volumeTemplates,
		},
	}}
	return obj, nil
}

func buildIsolatedSandboxEnv(instance *models.Instance, runtimeEnv, gatewayEnv, agentEnv map[string]string, proxyURL string) ([]corev1.EnvVar, error) {
	if instance == nil {
		return nil, fmt.Errorf("instance is required")
	}
	overrides, err := parseEnvironmentOverridesJSON(instance.EnvironmentOverridesJSON)
	if err != nil {
		return nil, err
	}
	overrides = stripDesktopEnv(overrides)
	resolved := mergeEnvMaps(runtimeEnv, mergeEnvMaps(gatewayEnv, agentEnv))
	resolved["CLAWMANAGER_RUNTIME_TYPE"] = RuntimeBackendGateway
	resolved = mergeEnvMaps(resolved, overrides)
	resolved = stripDesktopEnv(resolved)
	resolved = withRequiredProxyEnv(resolved, proxyURL)
	return envMapToVars(resolved), nil
}

func isolatedBaseRuntimeEnv(instance *models.Instance) map[string]string {
	env := map[string]string{
		"HOME":                     isolatedConfigMountPath,
		"RUNTIME_WORKSPACE_ROOT":   isolatedWorkspaceMountPath,
		"CLAWMANAGER_GATEWAY_PORT": strconv.Itoa(int(isolatedGatewayPort)),
	}
	if instance != nil && strings.EqualFold(instance.Type, RuntimeTypeHermes) {
		env["HERMES_HOME"] = isolatedConfigMountPath + "/.hermes"
		env["HOST"] = "0.0.0.0"
		env["HERMES_ACCEPT_HOOKS"] = "1"
	}
	return env
}

func isolatedGatewayCommand(instanceType string) []string {
	if strings.EqualFold(instanceType, RuntimeTypeHermes) {
		return []string{"/bin/sh", "-lc", "mkdir -p /workspaces /config/.hermes && exec /usr/local/bin/start-hermes-dashboard-gateway"}
	}
	return []string{"/bin/sh", "-lc", "mkdir -p /workspaces /config/.openclaw && exec /usr/local/bin/openclaw gateway run --allow-unconfigured --auth token --token \"${CLAWMANAGER_INSTANCE_TOKEN}\" --bind lan --port 19090 --force --dev --verbose"}
}

func isolatedGatewayImage(instanceType string) string {
	if selection, ok := runtimeImageOverrideForRuntimeType(instanceType, RuntimeBackendGateway); ok {
		return strings.TrimSpace(selection.Image)
	}
	return strings.TrimSpace(defaultGatewaySystemImageSettings[strings.ToLower(strings.TrimSpace(instanceType))])
}

func rejectIsolatedCustomImage(registry, tag *string) error {
	if stringPtrHasValue(registry) || stringPtrHasValue(tag) {
		return fmt.Errorf("isolated mode can only use platform images; custom image_registry/image_tag are not allowed")
	}
	return nil
}

func stringPtrHasValue(value *string) bool {
	return value != nil && strings.TrimSpace(*value) != ""
}

func isolatedResourceRequirements(instance *models.Instance) corev1.ResourceRequirements {
	resources := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(fmt.Sprintf("%g", instance.CPUCores)),
			corev1.ResourceMemory: resource.MustParse(fmt.Sprintf("%dGi", instance.MemoryGB)),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(fmt.Sprintf("%g", instance.CPUCores)),
			corev1.ResourceMemory: resource.MustParse(fmt.Sprintf("%dGi", instance.MemoryGB)),
		},
	}
	if instance.GPUEnabled && instance.GPUCount > 0 {
		gpu := resource.MustParse(strconv.Itoa(instance.GPUCount))
		resources.Requests["nvidia.com/gpu"] = gpu
		resources.Limits["nvidia.com/gpu"] = gpu
	}
	return resources
}

func isolatedVolumeClaimTemplates(instance *models.Instance, storageClass string) ([]any, error) {
	workspace, err := pvcTemplateToMap("workspace", storageClass, fmt.Sprintf("%dGi", instance.DiskGB))
	if err != nil {
		return nil, err
	}
	config, err := pvcTemplateToMap("config", storageClass, "1Gi")
	if err != nil {
		return nil, err
	}
	return []any{workspace, config}, nil
}

func pvcTemplateToMap(name, storageClass, storage string) (map[string]any, error) {
	mode := corev1.PersistentVolumeFilesystem
	pvc := corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse(storage)},
			},
			VolumeMode: &mode,
		},
	}
	if strings.TrimSpace(storageClass) != "" {
		class := strings.TrimSpace(storageClass)
		pvc.Spec.StorageClassName = &class
	}
	rendered, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&pvc)
	if err != nil {
		return nil, fmt.Errorf("failed to render isolated Sandbox PVC template: %w", err)
	}
	return rendered, nil
}

func isolatedSandboxLabels(instance *models.Instance, name string) map[string]string {
	return map[string]string{
		"app":                       "clawreef",
		"app.kubernetes.io/name":    name,
		"app.kubernetes.io/part-of": "clawmanager",
		"instance-id":               strconv.Itoa(instance.ID),
		"user-id":                   strconv.Itoa(instance.UserID),
		"instance-type":             instance.Type,
		"runtime-type":              RuntimeBackendGateway,
		"instance-mode":             InstanceModeIsolated,
		"managed-by":                "clawreef",
	}
}

func isolatedPrometheusAnnotations() map[string]string {
	return map[string]string{
		"prometheus.io/scrape": "true",
		"prometheus.io/path":   "/metrics",
		"prometheus.io/port":   isolatedAgentPublicPort,
	}
}

func envMapToVars(env map[string]string) []corev1.EnvVar {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	vars := make([]corev1.EnvVar, 0, len(keys))
	for _, key := range keys {
		vars = append(vars, corev1.EnvVar{Name: key, Value: env[key]})
	}
	return vars
}

func envVarsToUnstructured(vars []corev1.EnvVar) ([]any, error) {
	rendered, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&corev1.Container{Env: vars})
	if err != nil {
		return nil, fmt.Errorf("failed to render isolated Sandbox env: %w", err)
	}
	items, ok, err := unstructured.NestedSlice(rendered, "env")
	if err != nil {
		return nil, fmt.Errorf("failed to render isolated Sandbox env: %w", err)
	}
	if !ok {
		return []any{}, nil
	}
	return items, nil
}

func stripDesktopEnv(env map[string]string) map[string]string {
	if len(env) == 0 {
		return env
	}
	stripped := mergeEnvMaps(env, nil)
	for _, key := range selkiesDesktopStreamEnvKeys {
		delete(stripped, key)
	}
	for _, key := range []string{"TITLE", "SUBFOLDER", "KASM_SVC_SEND_CUT_TEXT", "KASM_SVC_ACCEPT_CUT_TEXT"} {
		delete(stripped, key)
	}
	return stripped
}

func normalizedGatewayContainerName(instanceType string) string {
	if strings.EqualFold(instanceType, RuntimeTypeHermes) {
		return RuntimeTypeHermes
	}
	return RuntimeTypeOpenClaw
}

func sandboxStateFromConditions(sandbox *unstructured.Unstructured) sandboxConditionState {
	condition, ok := latestSandboxCondition(sandbox)
	if !ok {
		return sandboxConditionState{Status: "creating", PodStatus: "pending"}
	}
	podStatus := condition.Type
	if condition.Reason != "" {
		podStatus += "/" + condition.Reason
	}
	switch condition.Type {
	case "Ready":
		if strings.EqualFold(condition.Status, "True") {
			return sandboxConditionState{Status: "running", PodStatus: podStatus, Reason: condition.Reason}
		}
	case "Suspended":
		if strings.EqualFold(condition.Status, "True") {
			return sandboxConditionState{Status: "stopped", PodStatus: podStatus, Reason: condition.Reason}
		}
	case "Finished":
		if strings.EqualFold(condition.Status, "True") {
			if condition.Reason == "PodFailed" {
				return sandboxConditionState{Status: "creating", PodStatus: podStatus, Reason: condition.Reason, Recreate: true}
			}
			message := firstNonEmptyString(condition.Message, condition.Reason, "Sandbox finished")
			return sandboxConditionState{Status: "error", PodStatus: podStatus, Reason: condition.Reason, ErrorMessage: &message}
		}
	}
	return sandboxConditionState{Status: "creating", PodStatus: podStatus, Reason: condition.Reason}
}

func latestSandboxCondition(sandbox *unstructured.Unstructured) (sandboxCondition, bool) {
	rawConditions, ok, _ := unstructured.NestedSlice(sandbox.Object, "status", "conditions")
	if !ok || len(rawConditions) == 0 {
		return sandboxCondition{}, false
	}
	var selected sandboxCondition
	selectedSet := false
	for idx, raw := range rawConditions {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		condition := sandboxCondition{
			Type:    stringField(item, "type"),
			Status:  stringField(item, "status"),
			Reason:  stringField(item, "reason"),
			Message: stringField(item, "message"),
			Index:   idx,
		}
		if condition.Type == "" {
			continue
		}
		condition.LastTransitionTime = timeField(item, "lastTransitionTime")
		condition.ObservedGeneration = int64Field(item, "observedGeneration")
		if !selectedSet || conditionNewer(condition, selected) {
			selected = condition
			selectedSet = true
		}
	}
	return selected, selectedSet
}

func conditionNewer(candidate, current sandboxCondition) bool {
	if !candidate.LastTransitionTime.IsZero() || !current.LastTransitionTime.IsZero() {
		if !candidate.LastTransitionTime.Equal(current.LastTransitionTime) {
			return candidate.LastTransitionTime.After(current.LastTransitionTime)
		}
	}
	if candidate.ObservedGeneration != current.ObservedGeneration {
		return candidate.ObservedGeneration > current.ObservedGeneration
	}
	return candidate.Index > current.Index
}

func firstSandboxPodIP(sandbox *unstructured.Unstructured) string {
	podIPs, ok, _ := unstructured.NestedStringSlice(sandbox.Object, "status", "podIPs")
	if ok && len(podIPs) > 0 {
		return strings.TrimSpace(podIPs[0])
	}
	return ""
}

func stringField(values map[string]any, key string) string {
	if value, ok := values[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func int64Field(values map[string]any, key string) int64 {
	switch value := values[key].(type) {
	case int64:
		return value
	case int:
		return int64(value)
	case int32:
		return int64(value)
	case float64:
		return int64(value)
	case string:
		parsed, _ := strconv.ParseInt(value, 10, 64)
		return parsed
	default:
		return 0
	}
}

func timeField(values map[string]any, key string) time.Time {
	switch value := values[key].(type) {
	case string:
		parsed, _ := time.Parse(time.RFC3339, value)
		return parsed
	case metav1.Time:
		return value.Time
	case time.Time:
		return value
	default:
		return time.Time{}
	}
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return map[string]string{}
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func stringMapToAny(values map[string]string) map[string]any {
	if len(values) == 0 {
		return map[string]any{}
	}
	converted := make(map[string]any, len(values))
	for key, value := range values {
		converted[key] = value
	}
	return converted
}

var _ RuntimeBackend = (*sandboxBackend)(nil)
