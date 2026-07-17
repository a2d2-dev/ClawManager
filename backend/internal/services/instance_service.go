package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"clawreef/internal/models"
	"clawreef/internal/repository"
	"clawreef/internal/services/k8s"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// InstanceService defines the interface for instance operations
type InstanceService interface {
	Create(userID int, req CreateInstanceRequest) (*models.Instance, error)
	ValidateCreateRequests(userID int, requests []CreateInstanceRequest) error
	GetByID(id int) (*models.Instance, error)
	GetByUserID(userID int, offset, limit int) ([]models.Instance, int, error)
	GetAllInstances(offset, limit int) ([]models.Instance, int, error)
	Start(instanceID int) error
	Stop(instanceID int) error
	Restart(instanceID int) error
	Delete(instanceID int) error
	Update(instanceID int, req UpdateInstanceRequest) error
	GetInstanceStatus(instanceID int) (*InstanceStatus, error)
	ForceSyncInstance(instanceID int) error
}

func (s *instanceService) ValidateCreateRequests(userID int, requests []CreateInstanceRequest) error {
	if len(requests) == 0 {
		return nil
	}
	for idx := range requests {
		requests[idx].Name = strings.TrimSpace(requests[idx].Name)
		if requests[idx].Name == "" {
			return fmt.Errorf("instance name is required")
		}
		environmentOverrides, err := normalizeEnvironmentOverrides(requests[idx].EnvironmentOverrides)
		if err != nil {
			return err
		}
		if _, err := marshalEnvironmentOverrides(environmentOverrides); err != nil {
			return err
		}
		if _, ok := normalizeDesktopStreamProfile(requests[idx].DesktopStreamProfile); !ok {
			return fmt.Errorf("invalid desktop stream profile")
		}
	}

	quota, err := s.quotaRepo.GetByUserID(userID)
	if err != nil {
		return fmt.Errorf("failed to get user quota: %w", err)
	}
	if quota == nil {
		return fmt.Errorf("user quota not found")
	}

	currentCount, err := s.instanceRepo.CountByUserID(userID)
	if err != nil {
		return fmt.Errorf("failed to count instances: %w", err)
	}
	if currentCount+len(requests) > quota.MaxInstances {
		return fmt.Errorf("instance limit reached: %d/%d", currentCount+len(requests), quota.MaxInstances)
	}

	existingInstances, err := s.instanceRepo.GetByUserID(userID, 0, 1000)
	if err != nil {
		return fmt.Errorf("failed to list user instances for quota validation: %w", err)
	}

	currentCPU := 0.0
	currentMemory := 0
	currentStorage := 0
	currentGPU := 0
	existingNames := map[string]struct{}{}
	for _, existing := range existingInstances {
		existingMode, err := modeForExistingInstance(&existing)
		if err != nil {
			return err
		}
		if instanceModeUsesDedicatedResources(existingMode) {
			currentCPU += existing.CPUCores
			currentMemory += existing.MemoryGB
			currentStorage += existing.DiskGB
			if existing.GPUEnabled {
				currentGPU += existing.GPUCount
			}
		}
		existingNames[strings.TrimSpace(strings.ToLower(existing.Name))] = struct{}{}
	}

	requestedCPU := 0.0
	requestedMemory := 0
	requestedStorage := 0
	requestedGPU := 0
	requestNames := map[string]struct{}{}
	for _, req := range requests {
		normalizedName := strings.TrimSpace(strings.ToLower(req.Name))
		if _, exists := existingNames[normalizedName]; exists {
			return fmt.Errorf("instance name already exists")
		}
		if _, exists := requestNames[normalizedName]; exists {
			return fmt.Errorf("instance name already exists")
		}
		requestNames[normalizedName] = struct{}{}
		instanceMode, err := resolveCreateInstanceMode(req)
		if err != nil {
			return err
		}
		if _, err := resolveCreateRuntimeType(req, instanceMode); err != nil {
			return err
		}
		if instanceModeUsesDedicatedResources(instanceMode) {
			requestedCPU += req.CPUCores
			requestedMemory += req.MemoryGB
			requestedStorage += req.DiskGB
			if req.GPUEnabled {
				requestedGPU += req.GPUCount
			}
		}
	}

	if currentCPU+requestedCPU > quota.MaxCPUCores {
		return fmt.Errorf("CPU cores exceed quota: current %v, requested %v, max %v", currentCPU, requestedCPU, quota.MaxCPUCores)
	}
	if currentMemory+requestedMemory > quota.MaxMemoryGB {
		return fmt.Errorf("memory exceed quota: current %dGB, requested %dGB, max %dGB", currentMemory, requestedMemory, quota.MaxMemoryGB)
	}
	if currentStorage+requestedStorage > quota.MaxStorageGB {
		return fmt.Errorf("storage exceed quota: current %dGB, requested %dGB, max %dGB", currentStorage, requestedStorage, quota.MaxStorageGB)
	}
	if currentGPU+requestedGPU > quota.MaxGPUCount {
		return fmt.Errorf("GPU count exceed quota: current %d, requested %d, max %d", currentGPU, requestedGPU, quota.MaxGPUCount)
	}

	return nil
}

// CreateInstanceRequest holds data for creating an instance
type CreateInstanceRequest struct {
	Name                 string              `json:"name" validate:"required,min=3,max=50"`
	Description          *string             `json:"description,omitempty"`
	Type                 string              `json:"type" validate:"required,oneof=openclaw ubuntu debian centos custom webtop hermes"`
	Mode                 string              `json:"mode" validate:"omitempty,oneof=lite isolated pro"`
	InstanceMode         string              `json:"instance_mode" validate:"omitempty,oneof=lite isolated pro"`
	RuntimeType          string              `json:"runtime_type" validate:"omitempty,oneof=gateway desktop shell"`
	DesktopStreamProfile string              `json:"desktop_stream_profile,omitempty" validate:"omitempty,oneof=low standard high"`
	CPUCores             float64             `json:"cpu_cores" validate:"required,min=0.1,max=32"`
	MemoryGB             int                 `json:"memory_gb" validate:"required,min=1,max=128"`
	DiskGB               int                 `json:"disk_gb" validate:"required,min=10,max=1000"`
	GPUEnabled           bool                `json:"gpu_enabled"`
	GPUCount             int                 `json:"gpu_count" validate:"min=0,max=4"`
	OSType               string              `json:"os_type" validate:"required"`
	OSVersion            string              `json:"os_version" validate:"required"`
	ImageRegistry        *string             `json:"image_registry,omitempty"`
	ImageTag             *string             `json:"image_tag,omitempty"`
	EnvironmentOverrides map[string]string   `json:"environment_overrides,omitempty"`
	StorageClass         string              `json:"storage_class"`
	OpenClawConfigPlan   *OpenClawConfigPlan `json:"openclaw_config_plan,omitempty"`
	Team                 *TeamInstanceConfig `json:"-"`
}

type TeamInstanceConfig struct {
	Environment      map[string]string
	SecretName       string
	SharedPVCName    string
	SharedMountPath  string
	ConfigMapName    string
	ConfigMountPath  string
	PersonaConfigKey string
	SharedUID        int64
	SharedGID        int64
	SharedUmask      string
}

type instanceModeLimitConfig struct {
	Capacity     *int
	MaxCPU       *float64
	MaxMemoryGB  *int
	MaxStorageGB *int
	MaxGPUCount  *int
}

// UpdateInstanceRequest holds data for updating an instance
type UpdateInstanceRequest struct {
	Name                 *string `json:"name,omitempty" validate:"omitempty,min=3,max=50"`
	Description          *string `json:"description,omitempty"`
	DesktopStreamProfile *string `json:"desktop_stream_profile,omitempty" validate:"omitempty,oneof=low standard high"`
}

// InstanceStatus holds the status of an instance
type InstanceStatus struct {
	InstanceID          int        `json:"instance_id"`
	Status              string     `json:"status"`
	Availability        string     `json:"availability,omitempty"`
	AgentType           string     `json:"agent_type,omitempty"`
	WorkspaceUsageBytes int64      `json:"workspace_usage_bytes,omitempty"`
	PodName             *string    `json:"pod_name,omitempty"`
	PodNamespace        *string    `json:"pod_namespace,omitempty"`
	PodIP               *string    `json:"pod_ip,omitempty"`
	PodStatus           string     `json:"pod_status,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	StartedAt           *time.Time `json:"started_at,omitempty"`
}

// instanceService implements InstanceService
type instanceService struct {
	instanceRepo          repository.InstanceRepository
	quotaRepo             repository.QuotaRepository
	llmModelRepo          repository.LLMModelRepository
	openClawConfigService OpenClawConfigService
	runtimeCapabilities   RuntimeCapabilities
	allowPrivilegedPods   bool
	runtimePodRepo        repository.RuntimePodRepository
	bindingRepo           repository.InstanceRuntimeBindingRepository
	agentClient           RuntimeAgentClient
	workspaceRoot         string
	podService            *k8s.PodService
	deploymentService     *k8s.InstanceDeploymentService
	pvcService            *k8s.PVCService
	serviceService        *k8s.ServiceService
	networkPolicyService  *k8s.NetworkPolicyService
	auditLogger           AuditLogger
}

type gatewayModelInjection struct {
	defaultModel string
	modelsJSON   string
}

type InstanceServiceOption func(*instanceService)

func WithPrivilegedInstancePods(allowed bool) InstanceServiceOption {
	return func(s *instanceService) {
		s.allowPrivilegedPods = allowed
	}
}

func WithV2RuntimeLifecycle(runtimePodRepo repository.RuntimePodRepository, bindingRepo repository.InstanceRuntimeBindingRepository, agentClient RuntimeAgentClient, workspaceRoot string) InstanceServiceOption {
	return func(s *instanceService) {
		s.runtimePodRepo = runtimePodRepo
		s.bindingRepo = bindingRepo
		s.agentClient = agentClient
		if strings.TrimSpace(workspaceRoot) != "" {
			s.workspaceRoot = strings.TrimSpace(workspaceRoot)
		}
	}
}

func WithRuntimeCapabilities(capabilities RuntimeCapabilities) InstanceServiceOption {
	return func(s *instanceService) {
		s.runtimeCapabilities = normalizeRuntimeCapabilities(capabilities)
	}
}

func WithInstanceAuditLogger(logger AuditLogger) InstanceServiceOption {
	return func(s *instanceService) {
		s.auditLogger = logger
	}
}

// NewInstanceService creates a new instance service
func NewInstanceService(instanceRepo repository.InstanceRepository, quotaRepo repository.QuotaRepository, llmModelRepo repository.LLMModelRepository, openClawConfigService OpenClawConfigService, options ...InstanceServiceOption) InstanceService {
	service := &instanceService{
		instanceRepo:          instanceRepo,
		quotaRepo:             quotaRepo,
		llmModelRepo:          llmModelRepo,
		openClawConfigService: openClawConfigService,
		workspaceRoot:         "/workspaces",
		podService:            k8s.NewPodService(),
		deploymentService:     k8s.NewInstanceDeploymentService(),
		pvcService:            k8s.NewPVCService(),
		serviceService:        k8s.NewServiceService(),
		networkPolicyService:  k8s.NewNetworkPolicyService(),
		auditLogger:           NewAuditLoggerFromEnv(),
	}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service
}

// Create creates a new instance
func (s *instanceService) Create(userID int, req CreateInstanceRequest) (*models.Instance, error) {
	ctx := context.Background()
	requestedMode := auditCreateInstanceMode(req)
	req.Name = strings.TrimSpace(req.Name)
	req.Type = strings.ToLower(strings.TrimSpace(req.Type))
	environmentOverrides, err := normalizeEnvironmentOverrides(req.EnvironmentOverrides)
	if err != nil {
		s.emitCreateRefused(userID, requestedMode, req, "validation_failed")
		return nil, err
	}
	if profile, ok := normalizeDesktopStreamProfile(req.DesktopStreamProfile); !ok {
		s.emitCreateRefused(userID, requestedMode, req, "validation_failed")
		return nil, fmt.Errorf("invalid desktop stream profile")
	} else if profile != "" {
		environmentOverrides = applyDesktopStreamProfileEnv(environmentOverrides, profile)
	}
	environmentOverridesJSON, err := marshalEnvironmentOverrides(environmentOverrides)
	if err != nil {
		s.emitCreateRefused(userID, requestedMode, req, "validation_failed")
		return nil, err
	}

	// Check user quota
	quota, err := s.quotaRepo.GetByUserID(userID)
	if err != nil {
		s.emitCreateRefused(userID, requestedMode, req, "quota_lookup_failed")
		return nil, fmt.Errorf("failed to get user quota: %w", err)
	}

	if quota == nil {
		s.emitCreateRefused(userID, requestedMode, req, "quota_missing")
		return nil, fmt.Errorf("user quota not found")
	}
	instanceMode, err := resolveCreateInstanceMode(req)
	if err != nil {
		s.emitCreateRefused(userID, requestedMode, req, refusalCodeForError(err))
		return nil, err
	}
	modeRuntimeType, err := resolveCreateRuntimeType(req, instanceMode)
	if err != nil {
		s.emitCreateRefused(userID, instanceMode, req, refusalCodeForError(err))
		return nil, err
	}
	if err := s.ensureInstanceModeAvailable(instanceMode); err != nil {
		s.emitCreateRefused(userID, instanceMode, req, refusalCodeForError(err))
		return nil, err
	}

	// Check instance count limit
	currentCount, err := s.instanceRepo.CountByUserID(userID)
	if err != nil {
		s.emitCreateRefused(userID, instanceMode, req, "quota_lookup_failed")
		return nil, fmt.Errorf("failed to count instances: %w", err)
	}

	if currentCount >= quota.MaxInstances {
		s.emitCreateRefused(userID, instanceMode, req, "quota_instance_limit")
		return nil, fmt.Errorf("instance limit reached: %d/%d", currentCount, quota.MaxInstances)
	}

	existingInstances, err := s.instanceRepo.GetByUserID(userID, 0, 1000)
	if err != nil {
		s.emitCreateRefused(userID, instanceMode, req, "quota_lookup_failed")
		return nil, fmt.Errorf("failed to list user instances for quota validation: %w", err)
	}

	currentCPU := 0.0
	currentMemory := 0
	currentStorage := 0
	currentGPU := 0
	for _, existing := range existingInstances {
		existingMode, err := modeForExistingInstance(&existing)
		if err != nil {
			s.emitCreateRefused(userID, instanceMode, req, refusalCodeForError(err))
			return nil, err
		}
		if instanceModeUsesDedicatedResources(existingMode) {
			currentCPU += existing.CPUCores
			currentMemory += existing.MemoryGB
			currentStorage += existing.DiskGB
			if existing.GPUEnabled {
				currentGPU += existing.GPUCount
			}
		}
	}

	nameExists, err := s.instanceRepo.ExistsByUserIDAndName(userID, req.Name)
	if err != nil {
		s.emitCreateRefused(userID, instanceMode, req, "validation_failed")
		return nil, fmt.Errorf("failed to validate instance name: %w", err)
	}
	if nameExists {
		s.emitCreateRefused(userID, instanceMode, req, "duplicate_name")
		return nil, fmt.Errorf("instance name already exists")
	}

	requestedGPU := 0
	if req.GPUEnabled {
		requestedGPU = req.GPUCount
	}
	if instanceModeUsesDedicatedResources(instanceMode) {
		// Check CPU limit
		if currentCPU+req.CPUCores > quota.MaxCPUCores {
			s.emitCreateRefused(userID, instanceMode, req, "quota_cpu_exceeded")
			return nil, fmt.Errorf("CPU cores exceed quota: current %v, requested %v, max %v", currentCPU, req.CPUCores, quota.MaxCPUCores)
		}

		// Check memory limit
		if currentMemory+req.MemoryGB > quota.MaxMemoryGB {
			s.emitCreateRefused(userID, instanceMode, req, "quota_memory_exceeded")
			return nil, fmt.Errorf("memory exceed quota: current %dGB, requested %dGB, max %dGB", currentMemory, req.MemoryGB, quota.MaxMemoryGB)
		}

		// Check storage limit
		if currentStorage+req.DiskGB > quota.MaxStorageGB {
			s.emitCreateRefused(userID, instanceMode, req, "quota_storage_exceeded")
			return nil, fmt.Errorf("storage exceed quota: current %dGB, requested %dGB, max %dGB", currentStorage, req.DiskGB, quota.MaxStorageGB)
		}

		// Check GPU limit
		if currentGPU+requestedGPU > quota.MaxGPUCount {
			s.emitCreateRefused(userID, instanceMode, req, "quota_gpu_exceeded")
			return nil, fmt.Errorf("GPU count exceed quota: current %d, requested %d, max %d", currentGPU, requestedGPU, quota.MaxGPUCount)
		}
	}
	if err := s.enforceInstanceModeLimits(ctx, instanceMode, req.CPUCores, req.MemoryGB, req.DiskGB, requestedGPU); err != nil {
		s.emitCreateRefused(userID, instanceMode, req, refusalCodeForError(err))
		return nil, err
	}
	backend, ok := s.runtimeBackendForMode(instanceMode)
	if !ok {
		s.emitCreateRefused(userID, instanceMode, req, "backend_unconfigured")
		return nil, fmt.Errorf("runtime backend %q is not configured for instance", instanceMode)
	}
	instance, err := backend.Create(ctx, userID, req, instanceMode, modeRuntimeType, environmentOverridesJSON)
	if err != nil {
		s.emitCreateRefused(userID, instanceMode, req, refusalCodeForError(err))
		return nil, err
	}
	s.emitInstanceLifecycle(AuditEventInstanceCreate, instance, AuditOutcomeSuccess, "")
	return instance, nil
}

// GetByID gets an instance by ID
func (s *instanceService) GetByID(id int) (*models.Instance, error) {
	instance, err := s.instanceRepo.GetByID(id)
	if err != nil {
		return nil, err
	}
	hydrateInstanceDesktopStreamProfile(instance)
	return instance, nil
}

// GetByUserID gets instances by user ID with pagination
func (s *instanceService) GetByUserID(userID int, offset, limit int) ([]models.Instance, int, error) {
	instances, err := s.instanceRepo.GetByUserID(userID, offset, limit)
	if err != nil {
		return nil, 0, err
	}
	hydrateInstancesDesktopStreamProfile(instances)

	total, err := s.instanceRepo.CountByUserID(userID)
	if err != nil {
		return nil, 0, err
	}

	return instances, total, nil
}

func (s *instanceService) GetAllInstances(offset, limit int) ([]models.Instance, int, error) {
	instances, err := s.instanceRepo.GetAll(offset, limit)
	if err != nil {
		return nil, 0, err
	}
	hydrateInstancesDesktopStreamProfile(instances)

	total, err := s.instanceRepo.CountAll()
	if err != nil {
		return nil, 0, err
	}

	return instances, total, nil
}

func hydrateInstancesDesktopStreamProfile(instances []models.Instance) {
	for idx := range instances {
		hydrateInstanceDesktopStreamProfile(&instances[idx])
	}
}

func hydrateInstanceDesktopStreamProfile(instance *models.Instance) {
	if instance == nil {
		return
	}
	environmentOverrides, err := parseEnvironmentOverridesJSON(instance.EnvironmentOverridesJSON)
	if err != nil {
		return
	}
	instance.DesktopStreamProfile = desktopStreamProfileFromEnv(environmentOverrides)
}

// Start starts an instance
func (s *instanceService) Start(instanceID int) error {
	ctx := context.Background()

	instance, err := s.instanceRepo.GetByID(instanceID)
	if err != nil {
		return fmt.Errorf("failed to get instance: %w", err)
	}

	if instance == nil {
		s.emitInstanceRefused(AuditEventInstanceStartRefused, nil, "not_found", map[string]interface{}{"requested_instance_id": instanceID})
		return fmt.Errorf("instance not found")
	}

	if instance.Status == "running" {
		s.emitInstanceRefused(AuditEventInstanceStartRefused, instance, "already_running", nil)
		return fmt.Errorf("instance is already running")
	}
	instanceMode, err := modeForExistingInstance(instance)
	if err != nil {
		s.emitInstanceRefused(AuditEventInstanceStartRefused, instance, refusalCodeForError(err), nil)
		return err
	}
	if err := s.enforceInstanceModeLimits(ctx, instanceMode, instance.CPUCores, instance.MemoryGB, instance.DiskGB, instance.GPUCount); err != nil {
		s.emitInstanceRefused(AuditEventInstanceStartRefused, instance, refusalCodeForError(err), nil)
		return err
	}

	if backend, runtimeType, ok, err := s.runtimeBackendForInstance(instance); err != nil {
		s.emitInstanceRefused(AuditEventInstanceStartRefused, instance, refusalCodeForError(err), nil)
		return err
	} else if ok {
		if err := backend.Start(ctx, instance, runtimeType); err != nil {
			s.emitInstanceRefused(AuditEventInstanceStartRefused, instance, refusalCodeForError(err), nil)
			return err
		}
		s.emitInstanceLifecycle(AuditEventInstanceStart, instance, AuditOutcomeSuccess, "")
		return nil
	}

	s.emitInstanceRefused(AuditEventInstanceStartRefused, instance, "backend_unconfigured", nil)
	return fmt.Errorf("runtime backend %q is not configured for instance", instanceMode)
}

func (s *instanceService) securityModeForInstance(instanceType string) k8s.PodSecurityMode {
	if s != nil && s.allowPrivilegedPods {
		return k8s.PodSecurityPrivileged
	}
	if strings.EqualFold(strings.TrimSpace(instanceType), "openclaw") {
		return k8s.PodSecurityChromiumCompat
	}
	return k8s.PodSecurityDefault
}

func (s *instanceService) ensureGatewayToken(instance *models.Instance) (string, error) {
	hadToken := instance != nil && instance.AccessToken != nil && strings.TrimSpace(*instance.AccessToken) != ""
	token, err := ensureGatewayTokenWithRepo(s.instanceRepo, instance)
	if err == nil && !hadToken {
		emitCredentialMinted(s.auditLogger, instance, "instance_gateway_token")
	}
	return token, err
}

func ensureGatewayTokenWithRepo(instanceRepo repository.InstanceRepository, instance *models.Instance) (string, error) {
	if instance.AccessToken != nil && strings.TrimSpace(*instance.AccessToken) != "" {
		return strings.TrimSpace(*instance.AccessToken), nil
	}

	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", fmt.Errorf("failed to generate instance gateway token: %w", err)
	}

	token := "igt_" + hex.EncodeToString(tokenBytes)
	instance.AccessToken = &token
	instance.UpdatedAt = time.Now()
	if err := instanceRepo.Update(instance); err != nil {
		return "", fmt.Errorf("failed to persist instance gateway token: %w", err)
	}

	return token, nil
}

func (s *instanceService) buildGatewayEnv(instance *models.Instance) (map[string]string, error) {
	if instance == nil || instance.AccessToken == nil || strings.TrimSpace(*instance.AccessToken) == "" {
		return map[string]string{}, nil
	}
	if !supportsManagedRuntimeIntegration(instance.Type) {
		return map[string]string{}, nil
	}

	baseURL, ok := defaultGatewayBaseURL()
	if !ok {
		return nil, fmt.Errorf("gateway base URL is not configured")
	}

	modelInjection, err := s.resolveGatewayModelInjection()
	if err != nil {
		return nil, err
	}

	token := strings.TrimSpace(*instance.AccessToken)
	return map[string]string{
		"CLAWMANAGER_LLM_BASE_URL":   baseURL,
		"CLAWMANAGER_LLM_API_KEY":    token,
		"CLAWMANAGER_LLM_MODEL":      modelInjection.modelsJSON,
		"CLAWMANAGER_LLM_PROVIDER":   "openai-compatible",
		"CLAWMANAGER_INSTANCE_TOKEN": token,
		"OPENAI_BASE_URL":            baseURL,
		"OPENAI_API_BASE":            baseURL,
		"OPENAI_API_KEY":             token,
		"OPENAI_MODEL":               modelInjection.defaultModel,
	}, nil
}

func (s *instanceService) BuildGatewayEnv(instance *models.Instance) (map[string]string, error) {
	if instance == nil || !supportsManagedRuntimeIntegration(instance.Type) {
		return s.buildGatewayEnv(instance)
	}
	if instance.AccessToken == nil || strings.TrimSpace(*instance.AccessToken) == "" {
		if s == nil || s.instanceRepo == nil {
			return nil, fmt.Errorf("instance repository is not configured")
		}
		if _, err := s.ensureGatewayToken(instance); err != nil {
			return nil, err
		}
	}
	gatewayEnv, err := s.buildGatewayEnv(instance)
	if err != nil {
		return nil, err
	}

	bootstrapEnv, err := s.runtimeBootstrapEnv(instance)
	if err != nil {
		return nil, err
	}

	agentEnv := map[string]string{}
	if _, ok := defaultAgentControlBaseURL(); ok {
		if instance.AgentBootstrapToken == nil || strings.TrimSpace(*instance.AgentBootstrapToken) == "" {
			if s == nil || s.instanceRepo == nil {
				return nil, fmt.Errorf("instance repository is not configured")
			}
			if _, err := s.ensureAgentBootstrapToken(instance); err != nil {
				return nil, err
			}
		}
		agentEnv, err = s.buildAgentEnv(instance)
		if err != nil {
			return nil, err
		}
	}

	merged := mergeEnvMaps(gatewayEnv, bootstrapEnv)
	merged = mergeEnvMaps(merged, agentEnv)
	return buildInstanceGatewayEnv(instance, merged)
}

func (s *instanceService) runtimeBootstrapEnv(instance *models.Instance) (map[string]string, error) {
	if instance == nil || s == nil || s.openClawConfigService == nil || instance.OpenClawConfigSnapshotID == nil || *instance.OpenClawConfigSnapshotID <= 0 {
		return map[string]string{}, nil
	}
	provider, ok := s.openClawConfigService.(interface {
		RuntimeEnvForSnapshot(userID int, instanceType string, snapshotID int) (map[string]string, error)
	})
	if !ok {
		return map[string]string{}, nil
	}
	return provider.RuntimeEnvForSnapshot(instance.UserID, instance.Type, *instance.OpenClawConfigSnapshotID)
}
func (s *instanceService) ensureAgentBootstrapToken(instance *models.Instance) (string, error) {
	hadToken := instance != nil && instance.AgentBootstrapToken != nil && strings.TrimSpace(*instance.AgentBootstrapToken) != ""
	token, err := ensureAgentBootstrapTokenWithRepo(s.instanceRepo, instance)
	if err == nil && !hadToken {
		emitCredentialMinted(s.auditLogger, instance, "agent_bootstrap_token")
	}
	return token, err
}

func ensureAgentBootstrapTokenWithRepo(instanceRepo repository.InstanceRepository, instance *models.Instance) (string, error) {
	if instance.AgentBootstrapToken != nil && strings.TrimSpace(*instance.AgentBootstrapToken) != "" {
		return strings.TrimSpace(*instance.AgentBootstrapToken), nil
	}

	token, err := generatePrefixedToken("agt_boot")
	if err != nil {
		return "", fmt.Errorf("failed to generate instance agent bootstrap token: %w", err)
	}
	instance.AgentBootstrapToken = &token
	instance.UpdatedAt = time.Now()
	if err := instanceRepo.Update(instance); err != nil {
		return "", fmt.Errorf("failed to persist instance agent bootstrap token: %w", err)
	}
	return token, nil
}

func (s *instanceService) buildAgentEnv(instance *models.Instance) (map[string]string, error) {
	if instance == nil || !supportsManagedRuntimeIntegration(instance.Type) {
		return map[string]string{}, nil
	}
	if instance.AgentBootstrapToken == nil || strings.TrimSpace(*instance.AgentBootstrapToken) == "" {
		return nil, fmt.Errorf("instance agent bootstrap token is not configured")
	}

	baseURL, ok := defaultAgentControlBaseURL()
	if !ok {
		return nil, fmt.Errorf("agent control base URL is not configured")
	}

	diskLimitBytes := int64(instance.DiskGB) * 1024 * 1024 * 1024

	return map[string]string{
		"CLAWMANAGER_AGENT_ENABLED":          "true",
		"CLAWMANAGER_AGENT_BASE_URL":         baseURL,
		"CLAWMANAGER_AGENT_BOOTSTRAP_TOKEN":  strings.TrimSpace(*instance.AgentBootstrapToken),
		"CLAWMANAGER_AGENT_DISK_LIMIT_BYTES": strconv.FormatInt(diskLimitBytes, 10),
		"CLAWMANAGER_AGENT_INSTANCE_ID":      fmt.Sprintf("%d", instance.ID),
		"CLAWMANAGER_AGENT_PERSISTENT_DIR":   managedRuntimePersistentDir(instance),
		"CLAWMANAGER_AGENT_PROTOCOL_VERSION": AgentProtocolVersionV1,
	}, nil
}

func supportsManagedRuntimeIntegration(instanceType string) bool {
	switch strings.ToLower(strings.TrimSpace(instanceType)) {
	case "openclaw", "hermes":
		return true
	default:
		return false
	}
}

func supportsRuntimeConfigInjection(instanceType string) bool {
	switch strings.ToLower(strings.TrimSpace(instanceType)) {
	case "openclaw", "hermes":
		return true
	default:
		return false
	}
}

func managedRuntimePersistentDir(instance *models.Instance) string {
	if instance == nil {
		return "/config"
	}
	if isLiteRuntimeInstance(instance) && instance.WorkspacePath != nil && strings.TrimSpace(*instance.WorkspacePath) != "" {
		workspacePath := strings.TrimSpace(*instance.WorkspacePath)
		if strings.EqualFold(instance.Type, "hermes") {
			return path.Join(workspacePath, "home", ".hermes")
		}
		return path.Join(workspacePath, "home", ".openclaw")
	}
	if strings.EqualFold(instance.Type, "hermes") {
		return "/config/.hermes"
	}
	return persistentVolumeMountPath(instance)
}
func persistentVolumeMountPath(instance *models.Instance) string {
	if instance == nil {
		return "/config"
	}
	if defaultPath := defaultMountPathForInstanceType(instance.Type); defaultPath == "/config" {
		return defaultPath
	}
	if strings.TrimSpace(instance.MountPath) != "" {
		return strings.TrimSpace(instance.MountPath)
	}
	return defaultMountPathForInstanceType(instance.Type)
}

func runtimeVolumeInitScripts(instanceType, mountPath string) []k8s.VolumeInitScript {
	if !strings.EqualFold(strings.TrimSpace(instanceType), "hermes") || strings.TrimSpace(mountPath) != "/config" {
		return nil
	}
	return []k8s.VolumeInitScript{
		{
			Name:      "data",
			MountPath: "/config",
			Script: `set -eu
base="${CLAWMANAGER_VOLUME_PATH:-/config}"
target="$base/.hermes"
if [ ! -d "$target" ]; then
  legacy_found=0
  for name in hermes-agent skills channels.json session.json bootstrap inventory.json; do
    if [ -e "$base/$name" ]; then legacy_found=1; fi
  done
  mkdir -p "$target"
  if [ "$legacy_found" = "1" ]; then
    for entry in "$base"/* "$base"/.[!.]* "$base"/..?*; do
      [ -e "$entry" ] || continue
      name="${entry##*/}"
      case "$name" in .|..|.hermes|Desktop|Downloads|lost+found) continue;; esac
      mv "$entry" "$target"/
    done
  fi
fi
chown -R 1000:1000 "$target" || true`,
		},
	}
}

func (s *instanceService) resolveGatewayModelInjection() (*gatewayModelInjection, error) {
	if s.llmModelRepo == nil {
		return nil, fmt.Errorf("llm model repository not configured")
	}

	items, err := s.llmModelRepo.ListActive()
	if err != nil {
		return nil, fmt.Errorf("failed to list active models: %w", err)
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("no active models are configured")
	}

	modelsForInjection := []string{"auto"}
	seen := map[string]struct{}{
		"auto": {},
	}

	for _, item := range items {
		displayName := strings.TrimSpace(item.DisplayName)
		if displayName == "" {
			displayName = strings.TrimSpace(item.ProviderModelName)
		}
		if displayName == "" {
			continue
		}

		normalizedName := strings.ToLower(displayName)
		if _, exists := seen[normalizedName]; exists {
			continue
		}
		seen[normalizedName] = struct{}{}
		modelsForInjection = append(modelsForInjection, displayName)
	}

	rawModels, err := json.Marshal(modelsForInjection)
	if err != nil {
		return nil, fmt.Errorf("failed to encode gateway model list: %w", err)
	}

	return &gatewayModelInjection{
		defaultModel: "auto",
		modelsJSON:   string(rawModels),
	}, nil
}

func mergeEnvMaps(base map[string]string, overlay map[string]string) map[string]string {
	merged := map[string]string{}
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range overlay {
		merged[key] = value
	}
	return merged
}

// Stop stops an instance
func (s *instanceService) Stop(instanceID int) error {
	ctx := context.Background()

	instance, err := s.instanceRepo.GetByID(instanceID)
	if err != nil {
		return fmt.Errorf("failed to get instance: %w", err)
	}

	if instance == nil {
		s.emitInstanceRefused(AuditEventInstanceStopRefused, nil, "not_found", map[string]interface{}{"requested_instance_id": instanceID})
		return fmt.Errorf("instance not found")
	}

	if backend, _, ok, err := s.runtimeBackendForInstance(instance); err != nil {
		s.emitInstanceRefused(AuditEventInstanceStopRefused, instance, refusalCodeForError(err), nil)
		return err
	} else if ok {
		if err := backend.Stop(ctx, instance); err != nil {
			s.emitInstanceRefused(AuditEventInstanceStopRefused, instance, refusalCodeForError(err), nil)
			return err
		}
		s.emitInstanceLifecycle(AuditEventInstanceStop, instance, AuditOutcomeSuccess, "")
		return nil
	}

	instanceMode, err := modeForExistingInstance(instance)
	if err != nil {
		s.emitInstanceRefused(AuditEventInstanceStopRefused, instance, refusalCodeForError(err), nil)
		return err
	}
	s.emitInstanceRefused(AuditEventInstanceStopRefused, instance, "backend_unconfigured", nil)
	return fmt.Errorf("runtime backend %q is not configured for instance", instanceMode)
}

// Restart restarts an instance
func (s *instanceService) Restart(instanceID int) error {
	ctx := context.Background()
	instance, err := s.instanceRepo.GetByID(instanceID)
	if err != nil {
		return fmt.Errorf("failed to get instance: %w", err)
	}
	if instance == nil {
		return fmt.Errorf("instance not found")
	}

	_, isV2 := v2RuntimeTypeForInstance(instance)
	waitForDesktopPods := !isV2 && instanceUsesDesktopRuntime(instance)

	if err := s.Stop(instanceID); err != nil {
		return fmt.Errorf("failed to stop instance: %w", err)
	}

	if waitForDesktopPods {
		if s.deploymentService == nil {
			return fmt.Errorf("instance deployment service is not configured")
		}
		if err := s.deploymentService.WaitForDeploymentPodsDeleted(ctx, instance.UserID, instance.ID); err != nil {
			return fmt.Errorf("failed waiting for desktop pods to stop: %w", err)
		}
	}

	if err := s.Start(instanceID); err != nil {
		return fmt.Errorf("failed to start instance: %w", err)
	}

	return nil
}

// Delete starts deleting an instance and all associated K8s resources.
func (s *instanceService) Delete(instanceID int) error {
	instance, err := s.instanceRepo.GetByID(instanceID)
	if err != nil {
		return fmt.Errorf("failed to get instance: %w", err)
	}

	if instance == nil {
		s.emitInstanceRefused(AuditEventInstanceDeleteRefused, nil, "not_found", map[string]interface{}{"requested_instance_id": instanceID})
		return fmt.Errorf("instance not found")
	}

	if backend, _, ok, err := s.runtimeBackendForInstance(instance); err != nil {
		s.emitInstanceRefused(AuditEventInstanceDeleteRefused, instance, refusalCodeForError(err), nil)
		return err
	} else if ok {
		if err := backend.Delete(context.Background(), instance); err != nil {
			s.emitInstanceRefused(AuditEventInstanceDeleteRefused, instance, refusalCodeForError(err), nil)
			return err
		}
		if _, async := backend.(*proBackend); !async {
			s.emitInstanceLifecycle(AuditEventInstanceDelete, instance, AuditOutcomeSuccess, "")
		}
		return nil
	}

	instanceMode, err := modeForExistingInstance(instance)
	if err != nil {
		s.emitInstanceRefused(AuditEventInstanceDeleteRefused, instance, refusalCodeForError(err), nil)
		return err
	}
	s.emitInstanceRefused(AuditEventInstanceDeleteRefused, instance, "backend_unconfigured", nil)
	return fmt.Errorf("runtime backend %q is not configured for instance", instanceMode)
}

// cleanupOrphanedResources cleans up any orphaned K8s resources for an instance
func (s *instanceService) cleanupOrphanedResources(ctx context.Context, userID, instanceID int) error {
	namespace := s.pvcService.GetClient().GetNamespace(userID)
	instanceLabel := fmt.Sprintf("%d", instanceID)
	client := s.pvcService.GetClient().Clientset

	// Check if namespace has other instances' pods
	allPods, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "managed-by=clawreef",
	})
	if err == nil {
		otheInstanceCount := 0
		for _, pod := range allPods.Items {
			if pod.Labels["instance-id"] != instanceLabel {
				otheInstanceCount++
			}
		}
		fmt.Printf("Namespace %s has %d other instance(s), will not delete namespace\n", namespace, otheInstanceCount)
	}

	deployments, err := client.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("instance-id=%s", instanceLabel),
	})
	if err == nil && len(deployments.Items) > 0 {
		for _, deployment := range deployments.Items {
			fmt.Printf("Deleting orphaned Deployment %s\n", deployment.Name)
			propagation := metav1.DeletePropagationForeground
			client.AppsV1().Deployments(namespace).Delete(ctx, deployment.Name, metav1.DeleteOptions{
				PropagationPolicy: &propagation,
			})
		}
	}

	// List and delete ConfigMaps with instance label
	configMaps, err := client.CoreV1().ConfigMaps(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("instance-id=%s", instanceLabel),
	})
	if err == nil && len(configMaps.Items) > 0 {
		for _, cm := range configMaps.Items {
			fmt.Printf("Deleting orphaned ConfigMap %s\n", cm.Name)
			client.CoreV1().ConfigMaps(namespace).Delete(ctx, cm.Name, metav1.DeleteOptions{})
		}
	}

	// List and delete Secrets with instance label
	secrets, err := client.CoreV1().Secrets(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("instance-id=%s", instanceLabel),
	})
	if err == nil && len(secrets.Items) > 0 {
		for _, secret := range secrets.Items {
			fmt.Printf("Deleting orphaned Secret %s\n", secret.Name)
			client.CoreV1().Secrets(namespace).Delete(ctx, secret.Name, metav1.DeleteOptions{})
		}
	}

	// List and delete Services with instance label
	services, err := client.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("instance-id=%s", instanceLabel),
	})
	if err == nil && len(services.Items) > 0 {
		for _, svc := range services.Items {
			fmt.Printf("Deleting orphaned Service %s\n", svc.Name)
			client.CoreV1().Services(namespace).Delete(ctx, svc.Name, metav1.DeleteOptions{})
		}
	}

	return nil
}

// cleanupOrphanedResourcesByUser cleans up any orphaned resources for a user that don't have corresponding DB records
// Update updates an instance
func (s *instanceService) Update(instanceID int, req UpdateInstanceRequest) error {
	instance, err := s.instanceRepo.GetByID(instanceID)
	if err != nil {
		return fmt.Errorf("failed to get instance: %w", err)
	}

	if instance == nil {
		return fmt.Errorf("instance not found")
	}

	// Update fields
	if req.Name != nil {
		instance.Name = *req.Name
	}
	if req.Description != nil {
		instance.Description = req.Description
	}
	if req.DesktopStreamProfile != nil {
		profile, ok := normalizeDesktopStreamProfile(*req.DesktopStreamProfile)
		if !ok || profile == "" {
			return fmt.Errorf("invalid desktop stream profile")
		}
		environmentOverrides, err := parseEnvironmentOverridesJSON(instance.EnvironmentOverridesJSON)
		if err != nil {
			return err
		}
		environmentOverrides = applyDesktopStreamProfileEnv(environmentOverrides, profile)
		environmentOverridesJSON, err := marshalEnvironmentOverrides(environmentOverrides)
		if err != nil {
			return err
		}
		instance.EnvironmentOverridesJSON = environmentOverridesJSON
	}

	instance.UpdatedAt = time.Now()

	if err := s.instanceRepo.Update(instance); err != nil {
		return fmt.Errorf("failed to update instance: %w", err)
	}

	return nil
}

// GetInstanceStatus gets the detailed status of an instance
func (s *instanceService) GetInstanceStatus(instanceID int) (*InstanceStatus, error) {
	ctx := context.Background()

	instance, err := s.instanceRepo.GetByID(instanceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get instance: %w", err)
	}

	if instance == nil {
		return nil, fmt.Errorf("instance not found")
	}

	if backend, _, ok, err := s.runtimeBackendForInstance(instance); err != nil {
		return nil, err
	} else if ok {
		return backend.Status(ctx, instance)
	}

	instanceMode, err := modeForExistingInstance(instance)
	if err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("runtime backend %q is not configured for instance", instanceMode)
}

// ForceSyncInstance forces a status sync for a single instance
func (s *instanceService) ForceSyncInstance(instanceID int) error {
	ctx := context.Background()

	instance, err := s.instanceRepo.GetByID(instanceID)
	if err != nil {
		return fmt.Errorf("failed to get instance: %w", err)
	}

	if instance == nil {
		return fmt.Errorf("instance not found")
	}

	if _, ok := v2RuntimeTypeForInstance(instance); ok {
		return nil
	}
	if instanceUsesDesktopRuntime(instance) {
		return s.forceSyncDeploymentInstance(ctx, instance)
	}

	fmt.Printf("Force syncing instance %d (current status: %s, user: %d)\n", instanceID, instance.Status, instance.UserID)

	// First try direct lookup by instance ID
	pod, err := s.podService.GetPod(ctx, instance.UserID, instance.ID)
	if err != nil {
		// Pod not found by instance ID, try to find by namespace scan
		fmt.Printf("Instance %d: Pod not found by ID, scanning namespace for any matching pods...\n", instanceID)

		namespace := s.pvcService.GetClient().GetNamespace(instance.UserID)
		pods, listErr := s.pvcService.GetClient().Clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: "managed-by=clawreef",
		})

		if listErr == nil && len(pods.Items) > 0 {
			// Try to find a pod that might belong to this instance by name pattern
			for _, p := range pods.Items {
				// Check if pod name contains instance ID
				if p.Labels["instance-id"] == fmt.Sprintf("%d", instanceID) {
					fmt.Printf("Instance %d: Found matching pod %s by label scan\n", instanceID, p.Name)
					pod = &p
					err = nil
					break
				}
			}
		}
	}

	if err != nil {
		fmt.Printf("Instance %d: Pod not found in K8s: %v\n", instanceID, err)

		deploymentExists, deploymentErr := s.podService.DeploymentExists(ctx, instance.UserID, instance.ID)
		if deploymentErr != nil {
			fmt.Printf("Instance %d: failed to check deployment while pod was missing: %v\n", instanceID, deploymentErr)
		}
		if deploymentExists {
			fmt.Printf("Instance %d: Deployment exists but no pod is available yet, updating to creating\n", instanceID)
			if instance.Status != "creating" {
				instance.Status = "creating"
				instance.PodName = nil
				instance.PodNamespace = nil
				instance.PodIP = nil
				instance.UpdatedAt = time.Now()

				if err := s.instanceRepo.Update(instance); err != nil {
					return fmt.Errorf("failed to update instance status: %w", err)
				}

				GetHub().BroadcastInstanceStatus(instance.UserID, instance)
			}
			return nil
		}

		// If instance thinks it's running or creating but pod doesn't exist, update to stopped
		if instance.Status == "running" || instance.Status == "creating" {
			fmt.Printf("Instance %d: Updating status from %s to stopped\n", instanceID, instance.Status)
			instance.Status = "stopped"
			instance.PodName = nil
			instance.PodNamespace = nil
			instance.PodIP = nil
			instance.UpdatedAt = time.Now()

			if err := s.instanceRepo.Update(instance); err != nil {
				return fmt.Errorf("failed to update instance status: %w", err)
			}

			// Broadcast status update
			GetHub().BroadcastInstanceStatus(instance.UserID, instance)
		}
		return nil
	}

	// Pod exists, sync status
	fmt.Printf("Instance %d: Pod found - %s (Status: %s, IP: %s)\n",
		instanceID, pod.Name, pod.Status.Phase, pod.Status.PodIP)

	needsUpdate := false

	// Check pod status
	if pod.Status.Phase == "Running" && instance.Status != "running" {
		fmt.Printf("Instance %d: Status mismatch - Pod Running but instance %s, updating\n", instanceID, instance.Status)
		instance.Status = "running"
		needsUpdate = true
	} else if pod.Status.Phase == "Pending" && instance.Status != "creating" {
		fmt.Printf("Instance %d: Status mismatch - Pod Pending but instance %s, updating\n", instanceID, instance.Status)
		instance.Status = "creating"
		needsUpdate = true
	}

	// Update Pod info if changed
	if instance.PodName == nil || *instance.PodName != pod.Name {
		instance.PodName = &pod.Name
		needsUpdate = true
	}
	if instance.PodNamespace == nil || *instance.PodNamespace != pod.Namespace {
		instance.PodNamespace = &pod.Namespace
		needsUpdate = true
	}
	if pod.Status.PodIP != "" && (instance.PodIP == nil || *instance.PodIP != pod.Status.PodIP) {
		instance.PodIP = &pod.Status.PodIP
		needsUpdate = true
	}

	if needsUpdate {
		instance.UpdatedAt = time.Now()
		if err := s.instanceRepo.Update(instance); err != nil {
			return fmt.Errorf("failed to update instance: %w", err)
		}

		fmt.Printf("Instance %d: Status updated to %s, broadcasting\n", instanceID, instance.Status)
		// Broadcast status update
		GetHub().BroadcastInstanceStatus(instance.UserID, instance)
	} else {
		fmt.Printf("Instance %d: Status already in sync (%s)\n", instanceID, instance.Status)
	}

	return nil
}

func (s *instanceService) forceSyncDeploymentInstance(ctx context.Context, instance *models.Instance) error {
	if s.deploymentService == nil {
		return fmt.Errorf("instance deployment service is not configured")
	}
	deployment, err := s.deploymentService.GetDeployment(ctx, instance.UserID, instance.ID)
	if err != nil {
		if instance.Status == "running" || instance.Status == "creating" {
			nextStatus := "stopped"
			if instance.Status == "creating" {
				nextStatus = "error"
			}
			instance.Status = nextStatus
			instance.PodName = nil
			instance.PodNamespace = nil
			instance.PodIP = nil
			instance.UpdatedAt = time.Now()
			if err := s.instanceRepo.Update(instance); err != nil {
				return fmt.Errorf("failed to update instance status: %w", err)
			}
			GetHub().BroadcastInstanceStatus(instance.UserID, instance)
		}
		return nil
	}

	needsUpdate := false
	desiredStatus := mapDeploymentToInstanceStatus(deployment)
	if instance.Status != desiredStatus {
		instance.Status = desiredStatus
		needsUpdate = true
	}
	if pod, podErr := s.deploymentService.GetActivePod(ctx, instance.UserID, instance.ID); podErr == nil && pod != nil {
		if pod.Status.PodIP != "" && (instance.PodIP == nil || *instance.PodIP != pod.Status.PodIP) {
			instance.PodIP = &pod.Status.PodIP
			needsUpdate = true
		}
		if instance.PodName == nil || *instance.PodName != pod.Name {
			instance.PodName = &pod.Name
			needsUpdate = true
		}
		if instance.PodNamespace == nil || *instance.PodNamespace != pod.Namespace {
			instance.PodNamespace = &pod.Namespace
			needsUpdate = true
		}
	}
	if needsUpdate {
		instance.UpdatedAt = time.Now()
		if err := s.instanceRepo.Update(instance); err != nil {
			return fmt.Errorf("failed to update instance: %w", err)
		}
		GetHub().BroadcastInstanceStatus(instance.UserID, instance)
	}
	return nil
}

func additionalServicePorts(primaryPort int32) []int32 {
	if primaryPort == 3000 || primaryPort == 8082 {
		return []int32{3000, 8082}
	}

	return nil
}

func normalizeInstanceRuntimeType(runtimeType string) string {
	switch strings.ToLower(strings.TrimSpace(runtimeType)) {
	case RuntimeBackendGateway:
		return RuntimeBackendGateway
	case "shell":
		return RuntimeBackendShell
	default:
		return RuntimeBackendDesktop
	}
}

func instanceUsesDesktopRuntime(instance *models.Instance) bool {
	if instance == nil {
		return true
	}
	return normalizeInstanceRuntimeType(instance.RuntimeType) == RuntimeBackendDesktop
}

func resolveCreateInstanceMode(req CreateInstanceRequest) (string, error) {
	modeRaw := strings.TrimSpace(req.Mode)
	instanceModeRaw := strings.TrimSpace(req.InstanceMode)
	if mode, ok := NormalizeInstanceMode(modeRaw); ok {
		if instanceModeRaw != "" {
			instanceMode, ok := NormalizeInstanceMode(instanceModeRaw)
			if !ok {
				return "", fmt.Errorf("unsupported instance mode %q", req.InstanceMode)
			}
			if instanceMode != mode {
				return "", fmt.Errorf("invalid instance mode/runtime_type combination: mode=%s conflicts with instance_mode=%s", mode, instanceMode)
			}
		}
		return mode, nil
	}
	if modeRaw != "" {
		return "", fmt.Errorf("unsupported instance mode %q", req.Mode)
	}
	if mode, ok := NormalizeInstanceMode(req.InstanceMode); ok {
		return mode, nil
	}
	if strings.TrimSpace(req.InstanceMode) != "" {
		return "", fmt.Errorf("unsupported instance mode %q", req.InstanceMode)
	}
	runtimeType, err := resolveRequestedRuntimeType(req.RuntimeType)
	if err != nil {
		return "", err
	}
	if runtimeType == RuntimeBackendDesktop || runtimeType == RuntimeBackendShell {
		return InstanceModePro, nil
	}
	return InstanceModeLite, nil
}

func resolveCreateRuntimeType(req CreateInstanceRequest, instanceMode string) (string, error) {
	runtimeType, err := resolveRequestedRuntimeType(req.RuntimeType)
	if err != nil {
		return "", err
	}
	if runtimeType != "" {
		if err := validateCreateModeRuntimeCombination(instanceMode, runtimeType); err != nil {
			return "", err
		}
		return runtimeType, nil
	}
	switch instanceMode {
	case InstanceModeLite, InstanceModeIsolated:
		return RuntimeBackendGateway, nil
	case InstanceModePro:
		return RuntimeBackendDesktop, nil
	default:
		return "", fmt.Errorf("unsupported instance mode %q", instanceMode)
	}
}

func validateCreateModeRuntimeCombination(instanceMode, runtimeType string) error {
	switch instanceMode {
	case InstanceModeLite:
		if runtimeType == RuntimeBackendGateway {
			return nil
		}
	case InstanceModePro:
		if runtimeType == RuntimeBackendDesktop || runtimeType == RuntimeBackendShell {
			return nil
		}
	case InstanceModeIsolated:
		if runtimeType == RuntimeBackendGateway {
			return nil
		}
	default:
		return fmt.Errorf("unsupported instance mode %q", instanceMode)
	}
	return fmt.Errorf("invalid instance mode/runtime_type combination: mode=%s runtime_type=%s", instanceMode, runtimeType)
}

func resolveRequestedRuntimeType(runtimeType string) (string, error) {
	raw := strings.TrimSpace(runtimeType)
	if raw == "" {
		return "", nil
	}
	switch strings.ToLower(raw) {
	case RuntimeBackendGateway:
		return RuntimeBackendGateway, nil
	case RuntimeBackendShell:
		return RuntimeBackendShell, nil
	case RuntimeBackendDesktop:
		return RuntimeBackendDesktop, nil
	default:
		return "", fmt.Errorf("unsupported runtime type %q", runtimeType)
	}
}

func modeForExistingInstance(instance *models.Instance) (string, error) {
	if instance == nil {
		return "", fmt.Errorf("invalid instance mode for instance_id=0: instance is nil; repair instance data before dispatch")
	}
	if mode, ok := NormalizeInstanceMode(instance.InstanceMode); ok {
		return mode, nil
	}
	return "", fmt.Errorf("invalid instance mode for instance_id=%d: instance_mode=%q; repair instance data before dispatch", instance.ID, instance.InstanceMode)
}

func instanceModeUsesDedicatedResources(mode string) bool {
	normalized, ok := NormalizeInstanceMode(mode)
	return ok && normalized != InstanceModeLite
}

func (s *instanceService) ensureInstanceModeAvailable(mode string) error {
	normalizedMode, ok := NormalizeInstanceMode(mode)
	if !ok {
		return fmt.Errorf("unsupported instance mode %q", mode)
	}
	if normalizedMode != InstanceModeIsolated {
		return nil
	}
	capability := normalizeRuntimeCapabilities(s.runtimeCapabilities).CapabilityForMode(normalizedMode)
	if capability.Available {
		return nil
	}
	reason := strings.TrimSpace(capability.Reason)
	if reason == "" {
		reason = "mode unavailable: isolated runtime requires agent-sandbox Sandbox CRD"
	}
	if !strings.Contains(strings.ToLower(reason), "mode unavailable") {
		reason = "mode unavailable: " + reason
	}
	return fmt.Errorf("%s", reason)
}

func (s *instanceService) enforceInstanceModeLimits(ctx context.Context, mode string, cpuCores float64, memoryGB, storageGB, gpuCount int) error {
	normalizedMode, ok := NormalizeInstanceMode(mode)
	if !ok {
		return fmt.Errorf("unsupported instance mode %q", mode)
	}
	limits := loadInstanceModeLimitConfig(normalizedMode)
	if limits.Capacity != nil {
		if *limits.Capacity <= 0 {
			return fmt.Errorf("%s instance mode is disabled", normalizedMode)
		}
		if s == nil || s.instanceRepo == nil {
			return fmt.Errorf("instance repository is not configured")
		}
		count, err := s.instanceRepo.CountActiveByMode(ctx, normalizedMode)
		if err != nil {
			return err
		}
		if count >= *limits.Capacity {
			return fmt.Errorf("%s instance capacity reached: %d/%d", normalizedMode, count, *limits.Capacity)
		}
	}
	if !instanceModeUsesDedicatedResources(normalizedMode) {
		return nil
	}
	if limits.MaxCPU != nil && cpuCores > *limits.MaxCPU {
		return fmt.Errorf("%s CPU cores exceed mode limit: requested %g, max %g", normalizedMode, cpuCores, *limits.MaxCPU)
	}
	if limits.MaxMemoryGB != nil && memoryGB > *limits.MaxMemoryGB {
		return fmt.Errorf("%s memory exceeds mode limit: requested %dGB, max %dGB", normalizedMode, memoryGB, *limits.MaxMemoryGB)
	}
	if limits.MaxStorageGB != nil && storageGB > *limits.MaxStorageGB {
		return fmt.Errorf("%s storage exceeds mode limit: requested %dGB, max %dGB", normalizedMode, storageGB, *limits.MaxStorageGB)
	}
	if limits.MaxGPUCount != nil && gpuCount > *limits.MaxGPUCount {
		return fmt.Errorf("%s GPU count exceeds mode limit: requested %d, max %d", normalizedMode, gpuCount, *limits.MaxGPUCount)
	}
	return nil
}

func loadInstanceModeLimitConfig(mode string) instanceModeLimitConfig {
	prefix := "CLAWMANAGER_" + strings.ToUpper(mode) + "_"
	return instanceModeLimitConfig{
		Capacity:     optionalIntEnv(prefix + "CAPACITY"),
		MaxCPU:       optionalFloatEnv(prefix + "MAX_CPU_CORES"),
		MaxMemoryGB:  optionalIntEnv(prefix + "MAX_MEMORY_GB"),
		MaxStorageGB: optionalIntEnv(prefix + "MAX_STORAGE_GB"),
		MaxGPUCount:  optionalIntEnv(prefix + "MAX_GPU_COUNT"),
	}
}

func optionalIntEnv(key string) *int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return nil
	}
	return &parsed
}

func optionalFloatEnv(key string) *float64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return nil
	}
	return &parsed
}

func (s *instanceService) runtimeWorkspaceRoot() string {
	if s != nil && strings.TrimSpace(s.workspaceRoot) != "" {
		return strings.TrimSpace(s.workspaceRoot)
	}
	return "/workspaces"
}

func trimOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
