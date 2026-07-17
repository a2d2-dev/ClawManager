package services

import (
	"context"
	"fmt"
	"strings"

	"clawreef/internal/models"
)

type isolatedGateBackend struct {
	capabilities RuntimeCapabilities
}

func newIsolatedGateBackend(s *instanceService) *isolatedGateBackend {
	if s == nil {
		return &isolatedGateBackend{capabilities: defaultRuntimeCapabilities()}
	}
	return &isolatedGateBackend{capabilities: normalizeRuntimeCapabilities(s.runtimeCapabilities)}
}

func (b *isolatedGateBackend) Create(ctx context.Context, userID int, req CreateInstanceRequest, instanceMode string, runtimeType string, environmentOverridesJSON *string) (*models.Instance, error) {
	if instanceMode != InstanceModeIsolated {
		return nil, fmt.Errorf("isolated runtime backend cannot create %s instances", instanceMode)
	}
	if runtimeType != RuntimeBackendGateway {
		return nil, fmt.Errorf("isolated instance mode requires runtime_type=gateway")
	}
	if err := b.ensureAvailable(); err != nil {
		return nil, err
	}
	return nil, errIsolatedBackendNotDelivered()
}

func (b *isolatedGateBackend) Start(ctx context.Context, instance *models.Instance, runtimeType string) error {
	if runtimeType != RuntimeBackendGateway {
		return fmt.Errorf("isolated instance mode requires runtime_type=gateway")
	}
	if err := b.ensureAvailable(); err != nil {
		return err
	}
	return errIsolatedBackendNotDelivered()
}

func (b *isolatedGateBackend) Stop(ctx context.Context, instance *models.Instance) error {
	if err := b.ensureAvailable(); err != nil {
		return err
	}
	return errIsolatedBackendNotDelivered()
}

func (b *isolatedGateBackend) Delete(ctx context.Context, instance *models.Instance) error {
	if err := b.ensureAvailable(); err != nil {
		return err
	}
	return errIsolatedBackendNotDelivered()
}

func (b *isolatedGateBackend) Status(ctx context.Context, instance *models.Instance) (*InstanceStatus, error) {
	if err := b.ensureAvailable(); err != nil {
		return nil, err
	}
	return nil, errIsolatedBackendNotDelivered()
}

func (b *isolatedGateBackend) Endpoint(ctx context.Context, instance *models.Instance) (*RuntimeEndpoint, error) {
	if err := b.ensureAvailable(); err != nil {
		return nil, err
	}
	return nil, errIsolatedBackendNotDelivered()
}

func (b *isolatedGateBackend) AttachPolicy(ctx context.Context, instance *models.Instance, policy RuntimePolicyAttachment) error {
	return errIsolatedBackendNotDelivered()
}

func (b *isolatedGateBackend) Suspend(ctx context.Context, instance *models.Instance) error {
	return errIsolatedBackendNotDelivered()
}

func (b *isolatedGateBackend) ensureAvailable() error {
	capability := normalizeRuntimeCapabilities(b.capabilities).CapabilityForMode(InstanceModeIsolated)
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

func errIsolatedBackendNotDelivered() error {
	return fmt.Errorf("isolated runtime backend is not delivered yet; follow-up issue #8 provides sandboxBackend")
}
