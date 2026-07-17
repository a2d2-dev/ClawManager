package services

import (
	"context"
	"fmt"
	"strings"
	"time"

	"clawreef/internal/services/k8s"
)

const (
	AgentSandboxGroup   = "agents.x-k8s.io"
	AgentSandboxCRDName = "sandboxes.agents.x-k8s.io"
)

type RuntimeModeCapability struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

type RuntimeCapabilities struct {
	InstanceModes map[string]RuntimeModeCapability `json:"instance_modes"`
	CheckedAt     time.Time                        `json:"checked_at"`
}

type RuntimeCapabilityService interface {
	GetRuntimeCapabilities(ctx context.Context) RuntimeCapabilities
}

type staticRuntimeCapabilityService struct {
	capabilities RuntimeCapabilities
}

func NewRuntimeCapabilityService(capabilities RuntimeCapabilities) RuntimeCapabilityService {
	return &staticRuntimeCapabilityService{capabilities: normalizeRuntimeCapabilities(capabilities)}
}

func (s *staticRuntimeCapabilityService) GetRuntimeCapabilities(ctx context.Context) RuntimeCapabilities {
	return normalizeRuntimeCapabilities(s.capabilities)
}

func ProbeRuntimeCapabilities(ctx context.Context) RuntimeCapabilities {
	capabilities := defaultRuntimeCapabilities()
	capabilities.InstanceModes[InstanceModeIsolated] = probeAgentSandboxCapability(ctx)
	return capabilities
}

func defaultRuntimeCapabilities() RuntimeCapabilities {
	now := time.Now().UTC()
	return RuntimeCapabilities{
		CheckedAt: now,
		InstanceModes: map[string]RuntimeModeCapability{
			InstanceModeLite: {
				Available: true,
			},
			InstanceModeIsolated: {
				Available: false,
				Reason:    fmt.Sprintf("mode unavailable: agent-sandbox Sandbox CRD %s has not been probed", AgentSandboxCRDName),
			},
			InstanceModePro: {
				Available: true,
			},
		},
	}
}

func normalizeRuntimeCapabilities(capabilities RuntimeCapabilities) RuntimeCapabilities {
	normalized := capabilities
	if normalized.CheckedAt.IsZero() {
		normalized.CheckedAt = time.Now().UTC()
	}
	if normalized.InstanceModes == nil {
		normalized.InstanceModes = map[string]RuntimeModeCapability{}
	}
	for mode, capability := range defaultRuntimeCapabilities().InstanceModes {
		if _, ok := normalized.InstanceModes[mode]; !ok {
			normalized.InstanceModes[mode] = capability
		}
	}
	return normalized
}

func probeAgentSandboxCapability(ctx context.Context) RuntimeModeCapability {
	client := k8s.GetClient()
	if client == nil || client.Clientset == nil {
		return RuntimeModeCapability{
			Available: false,
			Reason:    "mode unavailable: Kubernetes client is unavailable; cannot probe agent-sandbox Sandbox CRD",
		}
	}

	groups, err := client.Clientset.Discovery().ServerGroups()
	if err != nil {
		return RuntimeModeCapability{
			Available: false,
			Reason:    fmt.Sprintf("mode unavailable: failed to probe agent-sandbox Sandbox CRD %s: %v", AgentSandboxCRDName, err),
		}
	}

	for _, group := range groups.Groups {
		if group.Name != AgentSandboxGroup {
			continue
		}
		var probeErrors []string
		for _, version := range group.Versions {
			if err := ctx.Err(); err != nil {
				return RuntimeModeCapability{
					Available: false,
					Reason:    fmt.Sprintf("mode unavailable: failed to probe agent-sandbox Sandbox CRD %s: %v", AgentSandboxCRDName, err),
				}
			}
			resourceList, err := client.Clientset.Discovery().ServerResourcesForGroupVersion(version.GroupVersion)
			if err != nil {
				probeErrors = append(probeErrors, err.Error())
				continue
			}
			for _, resource := range resourceList.APIResources {
				if resource.Name == "sandboxes" {
					return RuntimeModeCapability{Available: true}
				}
			}
		}
		if len(probeErrors) > 0 {
			return RuntimeModeCapability{
				Available: false,
				Reason:    fmt.Sprintf("mode unavailable: failed to inspect agent-sandbox Sandbox CRD %s: %s", AgentSandboxCRDName, strings.Join(probeErrors, "; ")),
			}
		}
		break
	}

	return RuntimeModeCapability{
		Available: false,
		Reason:    fmt.Sprintf("mode unavailable: agent-sandbox Sandbox CRD %s is not installed", AgentSandboxCRDName),
	}
}

func (c RuntimeCapabilities) CapabilityForMode(mode string) RuntimeModeCapability {
	normalized, ok := NormalizeInstanceMode(mode)
	if !ok {
		return RuntimeModeCapability{
			Available: false,
			Reason:    fmt.Sprintf("unsupported instance mode %q", mode),
		}
	}
	c = normalizeRuntimeCapabilities(c)
	if capability, ok := c.InstanceModes[normalized]; ok {
		return capability
	}
	return RuntimeModeCapability{
		Available: false,
		Reason:    fmt.Sprintf("mode unavailable: %s is not available", normalized),
	}
}
