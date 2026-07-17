package services

import (
	"fmt"
	"path"
	"strings"
)

const (
	RuntimeTypeOpenClaw = "openclaw"
	RuntimeTypeHermes   = "hermes"

	InstanceModeLite     = "lite"
	InstanceModeIsolated = "isolated"
	InstanceModePro      = "pro"

	RuntimeBackendGateway = "gateway"
	RuntimeBackendDesktop = "desktop"
	RuntimeBackendShell   = "shell"

	RuntimeGatewayPortStart = 20000
	RuntimeGatewayPortEnd   = 20099
	RuntimePodCapacity      = 100
	RuntimeLinuxIDBase      = 200000
)

func NormalizeV2RuntimeType(instanceType string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(instanceType)) {
	case RuntimeTypeOpenClaw:
		return RuntimeTypeOpenClaw, true
	case RuntimeTypeHermes:
		return RuntimeTypeHermes, true
	default:
		return "", false
	}
}

func NormalizeInstanceMode(mode string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case InstanceModeLite:
		return InstanceModeLite, true
	case InstanceModeIsolated:
		return InstanceModeIsolated, true
	case InstanceModePro:
		return InstanceModePro, true
	default:
		return "", false
	}
}

func RuntimeWorkspacePath(runtimeType string, userID int, instanceID int) string {
	return RuntimeWorkspacePathWithRoot("/workspaces", runtimeType, userID, instanceID)
}

func RuntimeWorkspacePathWithRoot(root, runtimeType string, userID int, instanceID int) string {
	root = strings.TrimSpace(root)
	if root == "" {
		root = "/workspaces"
	}
	return path.Join(root, fmt.Sprintf("%s/user-%d/instance-%d", runtimeType, userID, instanceID))
}

func RuntimeLinuxID(instanceID int) int {
	return RuntimeLinuxIDBase + instanceID
}
