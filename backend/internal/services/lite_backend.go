package services

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"clawreef/internal/models"
)

func (s *instanceService) createV2Instance(ctx context.Context, userID int, req CreateInstanceRequest, runtimeType string, environmentOverridesJSON *string) (*models.Instance, error) {
	now := time.Now()
	workspaceRoot := s.runtimeWorkspaceRoot()
	instance := &models.Instance{
		UserID:                   userID,
		Name:                     strings.TrimSpace(req.Name),
		Description:              trimOptionalString(req.Description),
		Type:                     runtimeType,
		RuntimeType:              RuntimeBackendGateway,
		InstanceMode:             InstanceModeLite,
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

	if err := s.instanceRepo.Create(instance); err != nil {
		return nil, fmt.Errorf("failed to create instance record: %w", err)
	}

	if _, err := s.ensureGatewayToken(instance); err != nil {
		_ = s.instanceRepo.Delete(instance.ID)
		return nil, fmt.Errorf("failed to provision lite gateway token: %w", err)
	}
	if _, err := s.ensureAgentBootstrapToken(instance); err != nil {
		_ = s.instanceRepo.Delete(instance.ID)
		return nil, fmt.Errorf("failed to provision lite agent bootstrap token: %w", err)
	}

	if supportsRuntimeConfigInjection(instance.Type) && s.openClawConfigService != nil && req.OpenClawConfigPlan != nil && hasOpenClawConfigSelections(*req.OpenClawConfigPlan) {
		bootstrapSnapshot, err := s.openClawConfigService.CreateSnapshotForInstance(userID, instance, req.OpenClawConfigPlan)
		if err != nil {
			_ = s.instanceRepo.Delete(instance.ID)
			return nil, fmt.Errorf("failed to compile lite runtime bootstrap config: %w", err)
		}
		if bootstrapSnapshot != nil {
			instance.OpenClawConfigSnapshotID = &bootstrapSnapshot.ID
			instance.UpdatedAt = time.Now()
			if err := s.instanceRepo.Update(instance); err != nil {
				_ = s.openClawConfigService.MarkSnapshotFailed(bootstrapSnapshot, err)
				_ = s.instanceRepo.Delete(instance.ID)
				return nil, fmt.Errorf("failed to persist lite runtime snapshot reference: %w", err)
			}
			if err := s.openClawConfigService.MarkSnapshotActive(bootstrapSnapshot); err != nil {
				_ = s.instanceRepo.Delete(instance.ID)
				return nil, fmt.Errorf("failed to activate lite runtime bootstrap snapshot: %w", err)
			}
		}
	}

	workspacePath, err := ensureRuntimeWorkspaceDirectories(workspaceRoot, runtimeType, userID, instance.ID)
	if err != nil {
		_ = s.instanceRepo.Delete(instance.ID)
		return nil, fmt.Errorf("failed to create instance workspace: %w", err)
	}
	if err := s.instanceRepo.SetWorkspacePath(ctx, instance.ID, workspacePath); err != nil {
		_ = s.instanceRepo.Delete(instance.ID)
		return nil, fmt.Errorf("failed to persist instance workspace path: %w", err)
	}
	instance.WorkspacePath = &workspacePath

	GetHub().BroadcastInstanceStatus(userID, instance)
	return instance, nil
}

func (s *instanceService) startV2Instance(ctx context.Context, instance *models.Instance, runtimeType string) error {
	if err := s.ensureV2Workspace(ctx, instance, runtimeType); err != nil {
		return err
	}
	nextGeneration := instance.RuntimeGeneration + 1
	if nextGeneration <= 0 {
		nextGeneration = 1
	}
	if err := s.instanceRepo.UpdateRuntimeState(ctx, instance.ID, "creating", nextGeneration, nil); err != nil {
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

func (s *instanceService) stopV2Instance(ctx context.Context, instance *models.Instance) error {
	if err := s.instanceRepo.UpdateRuntimeState(ctx, instance.ID, "stopped", instance.RuntimeGeneration, nil); err != nil {
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
	return s.cleanupV2GatewayBinding(ctx, instance)
}

func (s *instanceService) deleteV2Instance(ctx context.Context, instance *models.Instance) error {
	if instance.Status != "deleting" {
		now := time.Now()
		instance.Status = "deleting"
		instance.UpdatedAt = now
		if err := s.instanceRepo.Update(instance); err != nil {
			return fmt.Errorf("failed to mark v2 instance as deleting: %w", err)
		}
		GetHub().BroadcastInstanceStatus(instance.UserID, instance)
	}

	cleanupErr := s.cleanupV2GatewayBinding(ctx, instance)
	if cleanupErr != nil {
		return cleanupErr
	}
	if err := s.instanceRepo.Delete(instance.ID); err != nil {
		return fmt.Errorf("failed to delete v2 instance record: %w", err)
	}
	return nil
}

func (s *instanceService) cleanupV2GatewayBinding(ctx context.Context, instance *models.Instance) error {
	if s.bindingRepo == nil {
		return nil
	}
	binding, err := s.bindingRepo.GetByInstanceID(ctx, instance.ID)
	if err != nil {
		return fmt.Errorf("failed to get v2 runtime binding: %w", err)
	}
	if binding == nil {
		return nil
	}

	if s.runtimePodRepo != nil {
		pod, podErr := s.runtimePodRepo.GetByID(ctx, binding.RuntimePodID)
		if podErr != nil {
			return fmt.Errorf("failed to get runtime pod %d for v2 cleanup: %w", binding.RuntimePodID, podErr)
		} else if pod == nil {
			return fmt.Errorf("runtime pod %d is not available for v2 cleanup", binding.RuntimePodID)
		} else if pod != nil && pod.AgentEndpoint != nil && strings.TrimSpace(*pod.AgentEndpoint) != "" && s.agentClient != nil && binding.GatewayID != "" {
			if err := s.agentClient.DeleteGateway(ctx, strings.TrimSpace(*pod.AgentEndpoint), binding.GatewayID); err != nil {
				return fmt.Errorf("failed to delete v2 gateway: %w", err)
			}
		}
	}

	if err := s.bindingRepo.DeleteByInstanceIDAndReleaseSlot(ctx, instance.ID, binding.RuntimePodID); err != nil {
		return fmt.Errorf("failed to delete v2 runtime binding and release slot: %w", err)
	}
	return nil
}

func (s *instanceService) ensureV2Workspace(ctx context.Context, instance *models.Instance, runtimeType string) error {
	if instance.WorkspacePath != nil && strings.TrimSpace(*instance.WorkspacePath) != "" {
		return nil
	}
	workspacePath, err := ensureRuntimeWorkspaceDirectories(s.runtimeWorkspaceRoot(), runtimeType, instance.UserID, instance.ID)
	if err != nil {
		return fmt.Errorf("failed to create instance workspace: %w", err)
	}
	if err := s.instanceRepo.SetWorkspacePath(ctx, instance.ID, workspacePath); err != nil {
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

func (s *instanceService) v2InstanceAvailability(ctx context.Context, instance *models.Instance) string {
	base := availabilityForStatus(instance.Status)
	if base != "available" {
		return base
	}
	if s == nil || s.bindingRepo == nil || s.runtimePodRepo == nil {
		return "unavailable"
	}
	binding, err := s.bindingRepo.GetRunningByInstanceID(ctx, instance.ID)
	if err != nil || binding == nil || binding.GatewayPort <= 0 {
		return "unavailable"
	}
	pod, err := s.runtimePodRepo.GetByID(ctx, binding.RuntimePodID)
	if err != nil || pod == nil || pod.PodIP == nil || strings.TrimSpace(*pod.PodIP) == "" {
		return "unavailable"
	}
	return "available"
}
