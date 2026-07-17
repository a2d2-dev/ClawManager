package services

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"clawreef/internal/models"
	"clawreef/internal/repository"
)

var ErrRuntimeSuspendUnsupported = errors.New("runtime suspend is not supported")

type RuntimeBackend interface {
	Create(ctx context.Context, userID int, req CreateInstanceRequest, instanceMode string, runtimeType string, environmentOverridesJSON *string) (*models.Instance, error)
	Start(ctx context.Context, instance *models.Instance, runtimeType string) error
	Stop(ctx context.Context, instance *models.Instance) error
	Delete(ctx context.Context, instance *models.Instance) error
	Status(ctx context.Context, instance *models.Instance) (*InstanceStatus, error)
	Endpoint(ctx context.Context, instance *models.Instance) (*RuntimeEndpoint, error)
	AttachPolicy(ctx context.Context, instance *models.Instance, policy RuntimePolicyAttachment) error
	Suspend(ctx context.Context, instance *models.Instance) error
}

type RuntimeEndpoint struct {
	AgentEndpoint string
	PodIP         string
	Port          int
}

type RuntimePolicyAttachment struct{}

type liteBackend struct {
	instanceRepo          repository.InstanceRepository
	runtimePodRepo        repository.RuntimePodRepository
	bindingRepo           repository.InstanceRuntimeBindingRepository
	agentClient           RuntimeAgentClient
	openClawConfigService OpenClawConfigService
	workspaceRoot         string
	auditLogger           AuditLogger
}

func newLiteBackend(s *instanceService) *liteBackend {
	workspaceRoot := "/workspaces"
	if s != nil && strings.TrimSpace(s.workspaceRoot) != "" {
		workspaceRoot = strings.TrimSpace(s.workspaceRoot)
	}
	if s == nil {
		return &liteBackend{workspaceRoot: workspaceRoot}
	}
	return &liteBackend{
		instanceRepo:          s.instanceRepo,
		runtimePodRepo:        s.runtimePodRepo,
		bindingRepo:           s.bindingRepo,
		agentClient:           s.agentClient,
		openClawConfigService: s.openClawConfigService,
		workspaceRoot:         workspaceRoot,
		auditLogger:           s.auditLogger,
	}
}

func (s *instanceService) runtimeBackendForMode(mode string) (RuntimeBackend, bool) {
	normalizedMode, ok := NormalizeInstanceMode(mode)
	if !ok {
		return nil, false
	}
	switch normalizedMode {
	case InstanceModeLite:
		return newLiteBackend(s), true
	case InstanceModeIsolated:
		return newSandboxBackend(s), true
	case InstanceModePro:
		return newProBackend(s), true
	default:
		return nil, false
	}
}

func (s *instanceService) runtimeBackendForInstance(instance *models.Instance) (RuntimeBackend, string, bool, error) {
	if instance == nil {
		return nil, "", false, fmt.Errorf("invalid instance mode for instance_id=0: instance is nil; repair instance data before dispatch")
	}
	mode, err := modeForExistingInstance(instance)
	if err != nil {
		return nil, "", false, err
	}
	backend, ok := s.runtimeBackendForMode(mode)
	if !ok {
		return nil, "", false, nil
	}
	return backend, normalizeInstanceRuntimeType(instance.RuntimeType), true, nil
}

func (b *liteBackend) Create(ctx context.Context, userID int, req CreateInstanceRequest, instanceMode string, runtimeType string, environmentOverridesJSON *string) (*models.Instance, error) {
	if instanceMode != InstanceModeLite {
		return nil, fmt.Errorf("lite runtime backend cannot create %s instances", instanceMode)
	}
	if runtimeType != RuntimeBackendGateway {
		return nil, fmt.Errorf("lite runtime backend requires runtime_type=gateway")
	}
	managedRuntimeType, ok := NormalizeV2RuntimeType(req.Type)
	if !ok {
		return nil, fmt.Errorf("lite runtime backend requires managed runtime type")
	}
	now := time.Now()
	workspaceRoot := b.runtimeWorkspaceRoot()
	instance := &models.Instance{
		UserID:                   userID,
		Name:                     strings.TrimSpace(req.Name),
		Description:              trimOptionalString(req.Description),
		Type:                     managedRuntimeType,
		RuntimeType:              RuntimeBackendGateway,
		InstanceMode:             instanceMode,
		Status:                   "creating",
		CPUCores:                 req.CPUCores,
		MemoryGB:                 req.MemoryGB,
		DiskGB:                   req.DiskGB,
		GPUEnabled:               req.GPUEnabled,
		GPUCount:                 req.GPUCount,
		OSType:                   req.OSType,
		OSVersion:                req.OSVersion,
		ImageRegistry:            req.ImageRegistry,
		ImageTag:                 req.ImageTag,
		EnvironmentOverridesJSON: environmentOverridesJSON,
		StorageClass:             strings.TrimSpace(req.StorageClass),
		MountPath:                workspaceRoot,
		RuntimeGeneration:        1,
		CreatedAt:                now,
		UpdatedAt:                now,
		StartedAt:                &now,
	}

	if err := b.instanceRepo.Create(instance); err != nil {
		return nil, fmt.Errorf("failed to create instance record: %w", err)
	}

	if _, err := b.ensureGatewayToken(instance); err != nil {
		_ = b.instanceRepo.Delete(instance.ID)
		return nil, fmt.Errorf("failed to provision lite gateway token: %w", err)
	}
	if _, err := b.ensureAgentBootstrapToken(instance); err != nil {
		_ = b.instanceRepo.Delete(instance.ID)
		return nil, fmt.Errorf("failed to provision lite agent bootstrap token: %w", err)
	}

	if supportsRuntimeConfigInjection(instance.Type) && b.openClawConfigService != nil && req.OpenClawConfigPlan != nil && hasOpenClawConfigSelections(*req.OpenClawConfigPlan) {
		bootstrapSnapshot, err := b.openClawConfigService.CreateSnapshotForInstance(userID, instance, req.OpenClawConfigPlan)
		if err != nil {
			_ = b.instanceRepo.Delete(instance.ID)
			return nil, fmt.Errorf("failed to compile lite runtime bootstrap config: %w", err)
		}
		if bootstrapSnapshot != nil {
			instance.OpenClawConfigSnapshotID = &bootstrapSnapshot.ID
			instance.UpdatedAt = time.Now()
			if err := b.instanceRepo.Update(instance); err != nil {
				_ = b.openClawConfigService.MarkSnapshotFailed(bootstrapSnapshot, err)
				_ = b.instanceRepo.Delete(instance.ID)
				return nil, fmt.Errorf("failed to persist lite runtime snapshot reference: %w", err)
			}
			if err := b.openClawConfigService.MarkSnapshotActive(bootstrapSnapshot); err != nil {
				_ = b.instanceRepo.Delete(instance.ID)
				return nil, fmt.Errorf("failed to activate lite runtime bootstrap snapshot: %w", err)
			}
		}
	}

	workspacePath, err := ensureRuntimeWorkspaceDirectories(workspaceRoot, managedRuntimeType, userID, instance.ID)
	if err != nil {
		_ = b.instanceRepo.Delete(instance.ID)
		return nil, fmt.Errorf("failed to create instance workspace: %w", err)
	}
	if err := b.instanceRepo.SetWorkspacePath(ctx, instance.ID, workspacePath); err != nil {
		_ = b.instanceRepo.Delete(instance.ID)
		return nil, fmt.Errorf("failed to persist instance workspace path: %w", err)
	}
	instance.WorkspacePath = &workspacePath

	GetHub().BroadcastInstanceStatus(userID, instance)
	return instance, nil
}

func (b *liteBackend) Start(ctx context.Context, instance *models.Instance, runtimeType string) error {
	if err := b.ensureWorkspace(ctx, instance, runtimeType); err != nil {
		return err
	}
	nextGeneration := instance.RuntimeGeneration + 1
	if nextGeneration <= 0 {
		nextGeneration = 1
	}
	if err := b.instanceRepo.UpdateRuntimeState(ctx, instance.ID, "creating", nextGeneration, nil); err != nil {
		return fmt.Errorf("failed to mark v2 instance creating: %w", err)
	}
	instance.Status = "creating"
	instance.RuntimeGeneration = nextGeneration
	instance.RuntimeErrorMessage = nil
	now := time.Now()
	instance.StartedAt = &now
	instance.UpdatedAt = now
	GetHub().BroadcastInstanceStatus(instance.UserID, instance)
	return nil
}

func (b *liteBackend) Stop(ctx context.Context, instance *models.Instance) error {
	if err := b.instanceRepo.UpdateRuntimeState(ctx, instance.ID, "stopped", instance.RuntimeGeneration, nil); err != nil {
		return fmt.Errorf("failed to mark v2 instance stopped: %w", err)
	}
	now := time.Now()
	instance.Status = "stopped"
	instance.StoppedAt = &now
	instance.PodName = nil
	instance.PodNamespace = nil
	instance.PodIP = nil
	instance.UpdatedAt = now
	GetHub().BroadcastInstanceStatus(instance.UserID, instance)
	return b.cleanupGatewayBinding(ctx, instance)
}

func (b *liteBackend) Delete(ctx context.Context, instance *models.Instance) error {
	if instance.Status != "deleting" {
		now := time.Now()
		instance.Status = "deleting"
		instance.UpdatedAt = now
		if err := b.instanceRepo.Update(instance); err != nil {
			return fmt.Errorf("failed to mark v2 instance as deleting: %w", err)
		}
		GetHub().BroadcastInstanceStatus(instance.UserID, instance)
	}

	cleanupErr := b.cleanupGatewayBinding(ctx, instance)
	if cleanupErr != nil {
		return cleanupErr
	}
	if err := b.instanceRepo.Delete(instance.ID); err != nil {
		return fmt.Errorf("failed to delete v2 instance record: %w", err)
	}
	return nil
}

func (b *liteBackend) Status(ctx context.Context, instance *models.Instance) (*InstanceStatus, error) {
	runtimeType, _ := NormalizeV2RuntimeType(instance.Type)
	return &InstanceStatus{
		InstanceID:          instance.ID,
		Status:              instance.Status,
		Availability:        b.availability(ctx, instance),
		AgentType:           runtimeType,
		WorkspaceUsageBytes: instance.WorkspaceUsageBytes,
		CreatedAt:           instance.CreatedAt,
		StartedAt:           instance.StartedAt,
	}, nil
}

func (b *liteBackend) Endpoint(ctx context.Context, instance *models.Instance) (*RuntimeEndpoint, error) {
	binding, pod, err := b.runningBindingAndPod(ctx, instance)
	if err != nil || binding == nil || pod == nil {
		return nil, err
	}
	endpoint := &RuntimeEndpoint{
		Port: binding.GatewayPort,
	}
	if pod.AgentEndpoint != nil {
		endpoint.AgentEndpoint = strings.TrimSpace(*pod.AgentEndpoint)
	}
	if pod.PodIP != nil {
		endpoint.PodIP = strings.TrimSpace(*pod.PodIP)
	}
	return endpoint, nil
}

func (b *liteBackend) AttachPolicy(ctx context.Context, instance *models.Instance, policy RuntimePolicyAttachment) error {
	return nil
}

func (b *liteBackend) Suspend(ctx context.Context, instance *models.Instance) error {
	return ErrRuntimeSuspendUnsupported
}

func (b *liteBackend) cleanupGatewayBinding(ctx context.Context, instance *models.Instance) error {
	if b.bindingRepo == nil {
		return nil
	}
	binding, err := b.bindingRepo.GetByInstanceID(ctx, instance.ID)
	if err != nil {
		return fmt.Errorf("failed to get v2 runtime binding: %w", err)
	}
	if binding == nil {
		return nil
	}

	if b.runtimePodRepo != nil {
		pod, podErr := b.runtimePodRepo.GetByID(ctx, binding.RuntimePodID)
		if podErr != nil {
			return fmt.Errorf("failed to get runtime pod %d for v2 cleanup: %w", binding.RuntimePodID, podErr)
		} else if pod == nil {
			return fmt.Errorf("runtime pod %d is not available for v2 cleanup", binding.RuntimePodID)
		} else if pod != nil && pod.AgentEndpoint != nil && strings.TrimSpace(*pod.AgentEndpoint) != "" && b.agentClient != nil && binding.GatewayID != "" {
			if err := b.agentClient.DeleteGateway(ctx, strings.TrimSpace(*pod.AgentEndpoint), binding.GatewayID); err != nil {
				return fmt.Errorf("failed to delete v2 gateway: %w", err)
			}
		}
	}

	if err := b.bindingRepo.DeleteByInstanceIDAndReleaseSlot(ctx, instance.ID, binding.RuntimePodID); err != nil {
		return fmt.Errorf("failed to delete v2 runtime binding and release slot: %w", err)
	}
	return nil
}

func (b *liteBackend) ensureWorkspace(ctx context.Context, instance *models.Instance, runtimeType string) error {
	if instance.WorkspacePath != nil && strings.TrimSpace(*instance.WorkspacePath) != "" {
		return nil
	}
	workspacePath, err := ensureRuntimeWorkspaceDirectories(b.runtimeWorkspaceRoot(), runtimeType, instance.UserID, instance.ID)
	if err != nil {
		return fmt.Errorf("failed to create instance workspace: %w", err)
	}
	if err := b.instanceRepo.SetWorkspacePath(ctx, instance.ID, workspacePath); err != nil {
		return fmt.Errorf("failed to persist instance workspace path: %w", err)
	}
	instance.WorkspacePath = &workspacePath
	return nil
}

func ensureRuntimeWorkspaceDirectories(root, runtimeType string, userID, instanceID int) (string, error) {
	workspacePath := RuntimeWorkspacePathWithRoot(root, runtimeType, userID, instanceID)
	if err := os.MkdirAll(workspacePath, 0750); err != nil {
		return "", err
	}

	// Allow the isolated gateway UID to traverse to its own workspace without
	// granting read/list access to sibling user or instance directories.
	userRoot := path.Dir(workspacePath)
	runtimeRoot := path.Dir(userRoot)
	for _, dir := range []string{runtimeRoot, userRoot} {
		if err := os.Chmod(dir, 0711); err != nil {
			return "", err
		}
	}
	if err := os.Chmod(workspacePath, 0750); err != nil {
		return "", err
	}
	return workspacePath, nil
}

func v2RuntimeTypeForInstance(instance *models.Instance) (string, bool) {
	if instance == nil {
		return "", false
	}
	runtimeType, ok := NormalizeV2RuntimeType(instance.Type)
	if !ok {
		return "", false
	}
	if strings.EqualFold(strings.TrimSpace(instance.RuntimeType), RuntimeBackendGateway) {
		return runtimeType, true
	}
	if mode, ok := NormalizeInstanceMode(instance.InstanceMode); ok && mode == InstanceModeLite {
		return runtimeType, true
	}
	return "", false
}

func availabilityForStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running":
		return "available"
	case "creating":
		return "starting"
	default:
		return "unavailable"
	}
}

func (b *liteBackend) availability(ctx context.Context, instance *models.Instance) string {
	base := availabilityForStatus(instance.Status)
	if base != "available" {
		return base
	}
	binding, pod, err := b.runningBindingAndPod(ctx, instance)
	if err != nil || binding == nil || pod == nil {
		return "unavailable"
	}
	return "available"
}

func (b *liteBackend) runningBindingAndPod(ctx context.Context, instance *models.Instance) (*models.InstanceRuntimeBinding, *models.RuntimePod, error) {
	if b == nil || b.bindingRepo == nil || b.runtimePodRepo == nil {
		return nil, nil, fmt.Errorf("lite runtime backend is not configured")
	}
	binding, err := b.bindingRepo.GetRunningByInstanceID(ctx, instance.ID)
	if err != nil || binding == nil || binding.GatewayPort <= 0 {
		return nil, nil, err
	}
	pod, err := b.runtimePodRepo.GetByID(ctx, binding.RuntimePodID)
	if err != nil || pod == nil || pod.PodIP == nil || strings.TrimSpace(*pod.PodIP) == "" {
		return nil, nil, err
	}
	return binding, pod, nil
}

func (b *liteBackend) runtimeWorkspaceRoot() string {
	if b != nil && strings.TrimSpace(b.workspaceRoot) != "" {
		return strings.TrimSpace(b.workspaceRoot)
	}
	return "/workspaces"
}

func (b *liteBackend) ensureGatewayToken(instance *models.Instance) (string, error) {
	hadToken := instance != nil && instance.AccessToken != nil && strings.TrimSpace(*instance.AccessToken) != ""
	token, err := ensureGatewayTokenWithRepo(b.instanceRepo, instance)
	if err == nil && !hadToken {
		emitCredentialMinted(b.auditLogger, instance, "instance_gateway_token")
	}
	return token, err
}

func (b *liteBackend) ensureAgentBootstrapToken(instance *models.Instance) (string, error) {
	hadToken := instance != nil && instance.AgentBootstrapToken != nil && strings.TrimSpace(*instance.AgentBootstrapToken) != ""
	token, err := ensureAgentBootstrapTokenWithRepo(b.instanceRepo, instance)
	if err == nil && !hadToken {
		emitCredentialMinted(b.auditLogger, instance, "agent_bootstrap_token")
	}
	return token, err
}
