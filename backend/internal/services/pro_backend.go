package services

import (
	"context"
	"fmt"
	"strings"
	"time"

	"clawreef/internal/models"
	"clawreef/internal/services/k8s"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type proBackend struct {
	service *instanceService
}

func newProBackend(s *instanceService) *proBackend {
	return &proBackend{service: s}
}

func (b *proBackend) Create(ctx context.Context, userID int, req CreateInstanceRequest, backendRuntimeType string, environmentOverridesJSON *string) (*models.Instance, error) {
	s := b.service
	if s == nil {
		return nil, fmt.Errorf("pro runtime backend is not configured")
	}
	runtimeConfig := buildRuntimeConfig(req.Type, req.OSType, req.OSVersion, req.ImageRegistry, req.ImageTag)
	backendRuntimeType = strings.TrimSpace(backendRuntimeType)
	runtimeType := normalizeInstanceRuntimeType(req.RuntimeType)
	if backendRuntimeType != "" {
		runtimeType = normalizeInstanceRuntimeType(backendRuntimeType)
	}
	if (req.ImageRegistry == nil || strings.TrimSpace(*req.ImageRegistry) == "") && (req.ImageTag == nil || strings.TrimSpace(*req.ImageTag) == "") {
		if selection, ok := runtimeImageOverride(req.Type); ok {
			image := selection.Image
			req.ImageRegistry = &image
			req.ImageTag = nil
			if backendRuntimeType == "" {
				runtimeType = normalizeInstanceRuntimeType(selection.RuntimeType)
			}
			runtimeConfig = buildRuntimeConfig(req.Type, req.OSType, req.OSVersion, req.ImageRegistry, req.ImageTag)
		}
	} else if req.ImageRegistry != nil {
		if selection, ok := runtimeImageOverrideForImage(req.Type, *req.ImageRegistry); ok {
			if backendRuntimeType == "" {
				runtimeType = normalizeInstanceRuntimeType(selection.RuntimeType)
			}
		}
	}

	// Check if there are any orphaned resources from previous failed creations
	fmt.Printf("Checking for orphaned resources for user %d before creating new instance...\n", userID)
	b.cleanupOrphanedResourcesByUser(ctx, userID)

	// Create instance record
	now := time.Now()
	instance := &models.Instance{
		UserID:                   userID,
		Name:                     req.Name,
		Description:              req.Description,
		Type:                     req.Type,
		RuntimeType:              runtimeType,
		InstanceMode:             InstanceModeForRuntimeType(runtimeType),
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
		StorageClass:             req.StorageClass,
		MountPath:                runtimeConfig.MountPath,
		CreatedAt:                now,
		UpdatedAt:                now,
	}

	if err := s.instanceRepo.Create(instance); err != nil {
		return nil, fmt.Errorf("failed to create instance record: %w", err)
	}

	if _, err := s.ensureGatewayToken(instance); err != nil {
		s.instanceRepo.Delete(instance.ID)
		return nil, fmt.Errorf("failed to provision instance gateway token: %w", err)
	}
	if _, err := s.ensureAgentBootstrapToken(instance); err != nil {
		s.instanceRepo.Delete(instance.ID)
		return nil, fmt.Errorf("failed to provision instance agent bootstrap token: %w", err)
	}

	gatewayEnv, err := s.buildGatewayEnv(instance)
	if err != nil {
		s.instanceRepo.Delete(instance.ID)
		return nil, fmt.Errorf("failed to build instance gateway config: %w", err)
	}
	agentEnv, err := s.buildAgentEnv(instance)
	if err != nil {
		s.instanceRepo.Delete(instance.ID)
		return nil, fmt.Errorf("failed to build instance agent config: %w", err)
	}
	extraEnv, err := buildInstancePodEnv(instance, runtimeConfig.Env, gatewayEnv, agentEnv)
	if err != nil {
		s.instanceRepo.Delete(instance.ID)
		return nil, fmt.Errorf("failed to resolve instance environment: %w", err)
	}
	if req.Team != nil {
		extraEnv = mergeEnvMaps(extraEnv, req.Team.Environment)
	}

	var bootstrapSnapshot *models.OpenClawInjectionSnapshot
	var bootstrapSecretName string
	if supportsRuntimeConfigInjection(instance.Type) && s.openClawConfigService != nil && req.OpenClawConfigPlan != nil && hasOpenClawConfigSelections(*req.OpenClawConfigPlan) {
		bootstrapSnapshot, err = s.openClawConfigService.CreateSnapshotForInstance(userID, instance, req.OpenClawConfigPlan)
		if err != nil {
			s.instanceRepo.Delete(instance.ID)
			return nil, fmt.Errorf("failed to compile runtime bootstrap config: %w", err)
		}
		if bootstrapSnapshot != nil {
			instance.OpenClawConfigSnapshotID = &bootstrapSnapshot.ID
			instance.UpdatedAt = time.Now()
			if err := s.instanceRepo.Update(instance); err != nil {
				s.instanceRepo.Delete(instance.ID)
				return nil, fmt.Errorf("failed to persist runtime snapshot reference: %w", err)
			}

			bootstrapSecretName, err = s.openClawConfigService.EnsureSnapshotSecret(ctx, userID, instance, bootstrapSnapshot.ID)
			if err != nil {
				_ = s.openClawConfigService.MarkSnapshotFailed(bootstrapSnapshot, err)
				s.instanceRepo.Delete(instance.ID)
				return nil, fmt.Errorf("failed to provision runtime bootstrap secret: %w", err)
			}
		}
	}

	// Create PVC
	// If storage class is not specified in request, use empty string
	// PVCService will use the default from K8s client config
	storageClass := req.StorageClass

	_, err = s.pvcService.CreatePVC(ctx, userID, instance.ID, req.DiskGB, storageClass)
	if err != nil {
		// Rollback: delete instance record
		if bootstrapSnapshot != nil {
			_ = s.openClawConfigService.MarkSnapshotFailed(bootstrapSnapshot, err)
		}
		s.instanceRepo.Delete(instance.ID)
		return nil, fmt.Errorf("failed to create PVC: %w", err)
	}

	nodeSelector, err := s.pvcService.NodeSelectorForPVC(ctx, userID, instance.ID, storageClass)
	if err != nil {
		if bootstrapSnapshot != nil {
			_ = s.openClawConfigService.MarkSnapshotFailed(bootstrapSnapshot, err)
		}
		s.pvcService.DeletePVC(ctx, userID, instance.ID)
		s.instanceRepo.Delete(instance.ID)
		return nil, fmt.Errorf("failed to resolve PVC node selector: %w", err)
	}

	// Ensure any legacy per-instance network policy is removed before creating pod.
	// This keeps new pods unrestricted even if older versions created netpols.
	if err := s.networkPolicyService.DeletePolicy(ctx, userID, instance.ID, instance.Name); err != nil {
		s.pvcService.DeletePVC(ctx, userID, instance.ID)
		if bootstrapSnapshot != nil {
			_ = s.openClawConfigService.MarkSnapshotFailed(bootstrapSnapshot, err)
		}
		s.instanceRepo.Delete(instance.ID)
		return nil, fmt.Errorf("failed to delete network policy: %w", err)
	}

	// Create Pod
	shmSizeGB := popSHMSizeGB(extraEnv, runtimeType, instance.MemoryGB)
	envFromSecretNames := []string{bootstrapSecretName}
	extraPVCMounts := []k8s.PVCMount{}
	configMapFileMounts := []k8s.ConfigMapFileMount{}
	volumeOwnershipFixes := []k8s.VolumeOwnershipFix{}
	var fsGroup *int64
	if req.Team != nil {
		if strings.TrimSpace(req.Team.SecretName) != "" {
			envFromSecretNames = append(envFromSecretNames, strings.TrimSpace(req.Team.SecretName))
		}
		if strings.TrimSpace(req.Team.SharedPVCName) != "" && strings.TrimSpace(req.Team.SharedMountPath) != "" {
			sharedMountPath := strings.TrimSpace(req.Team.SharedMountPath)
			extraPVCMounts = append(extraPVCMounts, k8s.PVCMount{
				Name:      "team-shared",
				ClaimName: strings.TrimSpace(req.Team.SharedPVCName),
				MountPath: sharedMountPath,
			})
			sharedUID := req.Team.SharedUID
			if sharedUID <= 0 {
				sharedUID = 1000
			}
			sharedGID := req.Team.SharedGID
			if sharedGID <= 0 {
				sharedGID = 1000
			}
			fsGroupValue := sharedGID
			fsGroup = &fsGroupValue
			volumeOwnershipFixes = append(volumeOwnershipFixes, k8s.VolumeOwnershipFix{
				Name:      "team-shared",
				MountPath: sharedMountPath,
				UID:       sharedUID,
				GID:       sharedGID,
			})
		}
		if strings.TrimSpace(req.Team.ConfigMapName) != "" && strings.TrimSpace(req.Team.ConfigMountPath) != "" {
			configMapFileMounts = append(configMapFileMounts, k8s.ConfigMapFileMount{
				Name:          "team-config",
				ConfigMapName: strings.TrimSpace(req.Team.ConfigMapName),
				Key:           "team.json",
				MountPath:     strings.TrimSpace(req.Team.ConfigMountPath),
				ReadOnly:      true,
				AsDirectory:   true,
			})
		}
		if strings.TrimSpace(req.Team.ConfigMapName) != "" && strings.TrimSpace(req.Team.PersonaConfigKey) != "" && strings.EqualFold(instance.Type, "hermes") {
			configMapFileMounts = append(configMapFileMounts, k8s.ConfigMapFileMount{
				Name:          "team-persona",
				ConfigMapName: strings.TrimSpace(req.Team.ConfigMapName),
				Key:           strings.TrimSpace(req.Team.PersonaConfigKey),
				MountPath:     teamHermesSoulMountPath,
				ReadOnly:      true,
			})
		}
	}

	podConfig := k8s.PodConfig{
		InstanceID:           instance.ID,
		InstanceName:         instance.Name,
		UserID:               userID,
		Type:                 instance.Type,
		RuntimeType:          runtimeType,
		CPUCores:             instance.CPUCores,
		MemoryGB:             instance.MemoryGB,
		GPUEnabled:           instance.GPUEnabled,
		GPUCount:             instance.GPUCount,
		Image:                runtimeConfig.Image,
		MountPath:            runtimeConfig.MountPath,
		ContainerPort:        runtimeConfig.Port,
		ImagePullPolicy:      corev1.PullPolicy(defaultImagePullPolicy()),
		ExtraEnv:             extraEnv,
		EnvFromSecretNames:   envFromSecretNames,
		ExtraPVCMounts:       extraPVCMounts,
		ConfigMapFileMounts:  configMapFileMounts,
		VolumeInitScripts:    runtimeVolumeInitScripts(instance.Type, runtimeConfig.MountPath),
		FSGroup:              fsGroup,
		NodeSelector:         nodeSelector,
		VolumeOwnershipFixes: volumeOwnershipFixes,
		SHMSizeGB:            shmSizeGB,
		SecurityMode:         s.securityModeForInstance(instance.Type),
	}

	var workloadNamespace string
	var workloadName string
	if instanceUsesDesktopRuntime(instance) {
		if s.deploymentService == nil {
			s.pvcService.DeletePVC(ctx, userID, instance.ID)
			if bootstrapSnapshot != nil {
				_ = s.openClawConfigService.MarkSnapshotFailed(bootstrapSnapshot, fmt.Errorf("instance deployment service is not configured"))
			}
			s.instanceRepo.Delete(instance.ID)
			return nil, fmt.Errorf("instance deployment service is not configured")
		}
		deployment, err := s.deploymentService.EnsureDeployment(ctx, podConfig, 1)
		if err != nil {
			s.pvcService.DeletePVC(ctx, userID, instance.ID)
			if bootstrapSnapshot != nil {
				_ = s.openClawConfigService.MarkSnapshotFailed(bootstrapSnapshot, err)
			}
			s.instanceRepo.Delete(instance.ID)
			return nil, fmt.Errorf("failed to create deployment: %w", err)
		}
		workloadNamespace = deployment.Namespace
		workloadName = deployment.Name

		// Create Service for browser desktop access.
		serviceConfig := k8s.ServiceConfig{
			InstanceID:      instance.ID,
			InstanceName:    instance.Name,
			UserID:          userID,
			ContainerPort:   runtimeConfig.Port,
			AdditionalPorts: additionalServicePorts(runtimeConfig.Port),
		}

		serviceInfo, err := s.serviceService.CreateService(ctx, serviceConfig)
		if err != nil {
			// Rollback: delete Deployment, PVC and instance record.
			_ = s.deploymentService.DeleteDeployment(ctx, userID, instance.ID)
			s.pvcService.DeletePVC(ctx, userID, instance.ID)
			if bootstrapSnapshot != nil {
				_ = s.openClawConfigService.MarkSnapshotFailed(bootstrapSnapshot, err)
			}
			s.instanceRepo.Delete(instance.ID)
			return nil, fmt.Errorf("failed to create service: %w", err)
		}

		fmt.Printf("Instance %d: Service created successfully (ClusterIP: %s)\n", instance.ID, serviceInfo.ClusterIP)
	} else {
		pod, err := s.podService.CreatePod(ctx, podConfig)
		if err != nil {
			// Rollback: delete PVC and instance record.
			s.pvcService.DeletePVC(ctx, userID, instance.ID)
			if bootstrapSnapshot != nil {
				_ = s.openClawConfigService.MarkSnapshotFailed(bootstrapSnapshot, err)
			}
			s.instanceRepo.Delete(instance.ID)
			return nil, fmt.Errorf("failed to create pod: %w", err)
		}
		workloadNamespace = pod.Namespace
		workloadName = pod.Name
		fmt.Printf("Instance %d: Shell runtime selected, skipping desktop service creation\n", instance.ID)
	}

	// Update instance with initial workload info. For Pro instances this is the
	// stable Deployment name; sync later records the active Pod name/IP.
	podNamespace := workloadNamespace
	podName := workloadName
	instance.PodNamespace = &podNamespace
	instance.PodName = &podName
	instance.Status = "creating"
	instance.StartedAt = &now
	instance.UpdatedAt = now

	fmt.Printf("Instance %d created successfully, updating database with status 'creating'\n", instance.ID)
	if err := s.instanceRepo.Update(instance); err != nil {
		if bootstrapSnapshot != nil {
			_ = s.openClawConfigService.MarkSnapshotFailed(bootstrapSnapshot, err)
		}
		return nil, fmt.Errorf("failed to update instance with pod info: %w", err)
	}
	fmt.Printf("Instance %d database updated, broadcasting status via WebSocket\n", instance.ID)

	if bootstrapSnapshot != nil {
		if err := s.openClawConfigService.MarkSnapshotActive(bootstrapSnapshot); err != nil {
			return nil, fmt.Errorf("failed to activate runtime bootstrap snapshot: %w", err)
		}
	}

	// Broadcast initial creating status via WebSocket. Sync service will mark it
	// running only after the pod becomes Ready.
	hydrateInstanceDesktopStreamProfile(instance)
	GetHub().BroadcastInstanceStatus(userID, instance)
	fmt.Printf("Instance %d status broadcast complete\n", instance.ID)

	return instance, nil
}

func (b *proBackend) Start(ctx context.Context, instance *models.Instance, _ string) error {
	s := b.service
	if s == nil {
		return fmt.Errorf("pro runtime backend is not configured")
	}
	if _, err := s.ensureGatewayToken(instance); err != nil {
		return fmt.Errorf("failed to provision instance gateway token: %w", err)
	}
	if _, err := s.ensureAgentBootstrapToken(instance); err != nil {
		return fmt.Errorf("failed to provision instance agent bootstrap token: %w", err)
	}

	gatewayEnv, err := s.buildGatewayEnv(instance)
	if err != nil {
		return fmt.Errorf("failed to build instance gateway config: %w", err)
	}
	agentEnv, err := s.buildAgentEnv(instance)
	if err != nil {
		return fmt.Errorf("failed to build instance agent config: %w", err)
	}
	runtimeConfig := buildRuntimeConfig(instance.Type, instance.OSType, instance.OSVersion, instance.ImageRegistry, instance.ImageTag)
	mountPath := persistentVolumeMountPath(instance)
	instance.MountPath = mountPath
	extraEnv, err := buildInstancePodEnv(instance, runtimeConfig.Env, gatewayEnv, agentEnv)
	if err != nil {
		return fmt.Errorf("failed to resolve instance environment: %w", err)
	}

	bootstrapSecretName := ""
	if supportsRuntimeConfigInjection(instance.Type) && s.openClawConfigService != nil && instance.OpenClawConfigSnapshotID != nil && *instance.OpenClawConfigSnapshotID > 0 {
		bootstrapSecretName, err = s.openClawConfigService.EnsureSnapshotSecret(ctx, instance.UserID, instance, *instance.OpenClawConfigSnapshotID)
		if err != nil {
			return fmt.Errorf("failed to restore runtime bootstrap secret: %w", err)
		}
	}

	// Remove legacy per-instance network policy before starting pod.
	if err := s.networkPolicyService.DeletePolicy(ctx, instance.UserID, instance.ID, instance.Name); err != nil {
		return fmt.Errorf("failed to delete network policy: %w", err)
	}

	runtimeType := normalizeInstanceRuntimeType(instance.RuntimeType)
	shmSizeGB := popSHMSizeGB(extraEnv, runtimeType, instance.MemoryGB)
	nodeSelector, err := s.pvcService.NodeSelectorForPVC(ctx, instance.UserID, instance.ID, instance.StorageClass)
	if err != nil {
		return fmt.Errorf("failed to resolve PVC node selector: %w", err)
	}
	podConfig := k8s.PodConfig{
		InstanceID:         instance.ID,
		InstanceName:       instance.Name,
		UserID:             instance.UserID,
		Type:               instance.Type,
		RuntimeType:        runtimeType,
		CPUCores:           instance.CPUCores,
		MemoryGB:           instance.MemoryGB,
		GPUEnabled:         instance.GPUEnabled,
		GPUCount:           instance.GPUCount,
		Image:              runtimeConfig.Image,
		MountPath:          mountPath,
		ContainerPort:      runtimeConfig.Port,
		ImagePullPolicy:    corev1.PullPolicy(defaultImagePullPolicy()),
		ExtraEnv:           extraEnv,
		EnvFromSecretNames: []string{bootstrapSecretName},
		VolumeInitScripts:  runtimeVolumeInitScripts(instance.Type, mountPath),
		NodeSelector:       nodeSelector,
		SHMSizeGB:          shmSizeGB,
		SecurityMode:       s.securityModeForInstance(instance.Type),
	}

	var workloadNamespace string
	var workloadName string
	if instanceUsesDesktopRuntime(instance) {
		if s.deploymentService == nil {
			return fmt.Errorf("instance deployment service is not configured")
		}
		deployment, err := s.deploymentService.EnsureDeployment(ctx, podConfig, 1)
		if err != nil {
			return fmt.Errorf("failed to ensure deployment: %w", err)
		}
		workloadNamespace = deployment.Namespace
		workloadName = deployment.Name

		// Ensure Service exists (create if not exists)
		serviceExists, _ := s.serviceService.ServiceExists(ctx, instance.UserID, instance.ID)
		if !serviceExists {
			serviceConfig := k8s.ServiceConfig{
				InstanceID:      instance.ID,
				InstanceName:    instance.Name,
				UserID:          instance.UserID,
				ContainerPort:   runtimeConfig.Port,
				AdditionalPorts: additionalServicePorts(runtimeConfig.Port),
			}
			_, err = s.serviceService.CreateService(ctx, serviceConfig)
			if err != nil {
				fmt.Printf("Warning: failed to create service for instance %d: %v\n", instance.ID, err)
				// Don't fail if service creation fails, pod is already running
			}
		}
	} else {
		pod, err := s.podService.CreatePod(ctx, podConfig)
		if err != nil {
			return fmt.Errorf("failed to create pod: %w", err)
		}
		workloadNamespace = pod.Namespace
		workloadName = pod.Name
	}

	// Update instance status
	now := time.Now()
	podNamespace := workloadNamespace
	podName := workloadName
	instance.PodNamespace = &podNamespace
	instance.PodName = &podName
	instance.Status = "creating"
	instance.StartedAt = &now
	instance.UpdatedAt = now

	if err := s.instanceRepo.Update(instance); err != nil {
		return fmt.Errorf("failed to update instance status: %w", err)
	}

	// Broadcast status update via WebSocket
	GetHub().BroadcastInstanceStatus(instance.UserID, instance)

	return nil
}

func (b *proBackend) Stop(ctx context.Context, instance *models.Instance) error {
	s := b.service
	if s == nil {
		return fmt.Errorf("pro runtime backend is not configured")
	}
	if instance.Status != "running" {
		return fmt.Errorf("instance is not running")
	}

	if instanceUsesDesktopRuntime(instance) {
		if s.deploymentService == nil {
			return fmt.Errorf("instance deployment service is not configured")
		}
		if err := s.deploymentService.ScaleDeployment(ctx, instance.UserID, instance.ID, 0); err != nil {
			fmt.Printf("Warning: failed to stop deployment for instance %d, falling back to pod delete: %v\n", instance.ID, err)
			if podErr := s.podService.DeletePod(ctx, instance.UserID, instance.ID); podErr != nil {
				return fmt.Errorf("failed to stop deployment: %w", err)
			}
		}
	} else {
		// Delete shell pod
		if err := s.podService.DeletePod(ctx, instance.UserID, instance.ID); err != nil {
			return fmt.Errorf("failed to delete pod: %w", err)
		}
	}

	// Update instance status
	now := time.Now()
	instance.Status = "stopped"
	instance.StoppedAt = &now
	instance.PodName = nil
	instance.PodNamespace = nil
	instance.PodIP = nil
	instance.UpdatedAt = now

	if err := s.instanceRepo.Update(instance); err != nil {
		return fmt.Errorf("failed to update instance status: %w", err)
	}

	// Broadcast status update via WebSocket
	GetHub().BroadcastInstanceStatus(instance.UserID, instance)

	return nil
}

func (b *proBackend) Delete(ctx context.Context, instance *models.Instance) error {
	s := b.service
	if s == nil {
		return fmt.Errorf("pro runtime backend is not configured")
	}
	if instance.Status != "deleting" {
		now := time.Now()
		instance.Status = "deleting"
		instance.UpdatedAt = now

		if err := s.instanceRepo.Update(instance); err != nil {
			return fmt.Errorf("failed to mark instance as deleting: %w", err)
		}

		GetHub().BroadcastInstanceStatus(instance.UserID, instance)
	}

	go b.completeDeletion(instance.UserID, instance.ID)

	return nil
}

func (b *proBackend) completeDeletion(userID, instanceID int) {
	s := b.service
	ctx := context.Background()

	fmt.Printf("Starting background deletion of instance %d (user %d)\n", instanceID, userID)

	// Use CleanupService to delete ALL resources for this instance (including duplicates)
	cleanupService := k8s.NewCleanupService()
	if err := cleanupService.DeleteAllInstanceResources(ctx, userID, instanceID); err != nil {
		fmt.Printf("Warning: error during resource cleanup for instance %d: %v\n", instanceID, err)
	}

	// Delete instance record from database after background cleanup finishes.
	fmt.Printf("Deleting instance %d from database...\n", instanceID)
	if err := s.instanceRepo.Delete(instanceID); err != nil {
		fmt.Printf("Error: failed to delete instance %d record: %v\n", instanceID, err)
		return
	}

	fmt.Printf("Instance %d deleted successfully\n", instanceID)
}

func (b *proBackend) cleanupOrphanedResourcesByUser(ctx context.Context, userID int) {
	s := b.service
	namespace := s.pvcService.GetClient().GetNamespace(userID)
	client := s.pvcService.GetClient().Clientset

	deployments, err := client.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "managed-by=clawreef",
	})
	if err != nil {
		fmt.Printf("Warning: failed to list deployments in namespace %s: %v\n", namespace, err)
	} else {
		for _, deployment := range deployments.Items {
			instanceIDStr := deployment.Labels["instance-id"]
			if instanceIDStr == "" {
				continue
			}

			instanceID := 0
			fmt.Sscanf(instanceIDStr, "%d", &instanceID)

			instance, err := s.instanceRepo.GetByID(instanceID)
			if err != nil || instance == nil {
				fmt.Printf("Found orphaned deployment %s (instance-id: %s), deleting...\n", deployment.Name, instanceIDStr)
				propagation := metav1.DeletePropagationForeground
				if err := client.AppsV1().Deployments(namespace).Delete(ctx, deployment.Name, metav1.DeleteOptions{
					PropagationPolicy: &propagation,
				}); err != nil {
					fmt.Printf("Warning: failed to delete orphaned deployment %s: %v\n", deployment.Name, err)
				}
			}
		}
	}

	// Get all pods in the namespace with clawreef label
	pods, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "managed-by=clawreef",
	})
	if err != nil {
		fmt.Printf("Warning: failed to list pods in namespace %s: %v\n", namespace, err)
		return
	}

	// For each pod, check if corresponding instance exists in DB
	for _, pod := range pods.Items {
		instanceIDStr := pod.Labels["instance-id"]
		if instanceIDStr == "" {
			continue
		}

		instanceID := 0
		fmt.Sscanf(instanceIDStr, "%d", &instanceID)

		// Check if instance exists in DB
		instance, err := s.instanceRepo.GetByID(instanceID)
		if err != nil || instance == nil {
			// Instance doesn't exist, this is an orphaned pod
			fmt.Printf("Found orphaned pod %s (instance-id: %s), deleting...\n", pod.Name, instanceIDStr)
			if err := client.CoreV1().Pods(namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{}); err != nil {
				fmt.Printf("Warning: failed to delete orphaned pod %s: %v\n", pod.Name, err)
			}
		}
	}

	// Also check PVCs
	pvcs, err := client.CoreV1().PersistentVolumeClaims(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "managed-by=clawreef",
	})
	if err != nil {
		fmt.Printf("Warning: failed to list PVCs in namespace %s: %v\n", namespace, err)
		return
	}

	for _, pvc := range pvcs.Items {
		instanceIDStr := pvc.Labels["instance-id"]
		if instanceIDStr == "" {
			continue
		}

		instanceID := 0
		fmt.Sscanf(instanceIDStr, "%d", &instanceID)

		// Check if instance exists in DB
		instance, err := s.instanceRepo.GetByID(instanceID)
		if err != nil || instance == nil {
			// Instance doesn't exist, this is an orphaned PVC
			fmt.Printf("Found orphaned PVC %s (instance-id: %s), deleting...\n", pvc.Name, instanceIDStr)
			if err := client.CoreV1().PersistentVolumeClaims(namespace).Delete(ctx, pvc.Name, metav1.DeleteOptions{}); err != nil {
				fmt.Printf("Warning: failed to delete orphaned PVC %s: %v\n", pvc.Name, err)
			}
			// Also try to delete the associated PV
			if pvc.Spec.VolumeName != "" {
				client.CoreV1().PersistentVolumes().Delete(ctx, pvc.Spec.VolumeName, metav1.DeleteOptions{})
			}
		}
	}

	networkPolicies, err := client.NetworkingV1().NetworkPolicies(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "managed-by=clawreef",
	})
	if err != nil {
		fmt.Printf("Warning: failed to list network policies in namespace %s: %v\n", namespace, err)
		return
	}

	for _, policy := range networkPolicies.Items {
		instanceIDStr := policy.Labels["instance-id"]
		if instanceIDStr == "" {
			continue
		}

		instanceID := 0
		fmt.Sscanf(instanceIDStr, "%d", &instanceID)

		instance, err := s.instanceRepo.GetByID(instanceID)
		if err != nil || instance == nil {
			fmt.Printf("Found orphaned NetworkPolicy %s (instance-id: %s), deleting...\n", policy.Name, instanceIDStr)
			if err := client.NetworkingV1().NetworkPolicies(namespace).Delete(ctx, policy.Name, metav1.DeleteOptions{}); err != nil {
				fmt.Printf("Warning: failed to delete orphaned NetworkPolicy %s: %v\n", policy.Name, err)
			}
		}
	}
}

func (b *proBackend) Status(ctx context.Context, instance *models.Instance) (*InstanceStatus, error) {
	s := b.service
	if s == nil {
		return nil, fmt.Errorf("pro runtime backend is not configured")
	}
	status := &InstanceStatus{
		InstanceID:   instance.ID,
		Status:       instance.Status,
		PodName:      instance.PodName,
		PodNamespace: instance.PodNamespace,
		PodIP:        instance.PodIP,
		CreatedAt:    instance.CreatedAt,
		StartedAt:    instance.StartedAt,
	}

	// Get pod status if running
	if instance.Status == "running" || instance.Status == "creating" {
		podStatus, err := s.podService.GetPodStatus(ctx, instance.UserID, instance.ID)
		if err == nil && podStatus != nil {
			status.PodStatus = string(podStatus.Phase)
		}
	}

	return status, nil
}

func (b *proBackend) Endpoint(ctx context.Context, instance *models.Instance) (*RuntimeEndpoint, error) {
	if instance == nil {
		return nil, fmt.Errorf("instance not found")
	}
	endpoint := &RuntimeEndpoint{Port: int(buildRuntimeConfig(instance.Type, instance.OSType, instance.OSVersion, instance.ImageRegistry, instance.ImageTag).Port)}
	if instance.PodIP != nil {
		endpoint.PodIP = strings.TrimSpace(*instance.PodIP)
	}
	return endpoint, nil
}

func (b *proBackend) AttachPolicy(ctx context.Context, instance *models.Instance, policy RuntimePolicyAttachment) error {
	return nil
}

func (b *proBackend) Suspend(ctx context.Context, instance *models.Instance) error {
	return ErrRuntimeSuspendUnsupported
}
