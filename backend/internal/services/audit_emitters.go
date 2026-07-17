package services

import (
	"strings"

	"clawreef/internal/models"
)

func (s *instanceService) emitCreateRefused(userID int, instanceMode string, req CreateInstanceRequest, refusalCode string) {
	if s == nil {
		return
	}
	emitAudit(s.auditLogger, AuditLogEvent{
		Event:        AuditEventInstanceCreateRefused,
		InstanceMode: normalizeAuditInstanceMode(instanceMode),
		InstanceID:   nil,
		UserID:       auditIntPtr(userID),
		Outcome:      AuditOutcomeRefused,
		RefusalCode:  refusalCode,
		Context: map[string]interface{}{
			"instance_name": strings.TrimSpace(req.Name),
			"instance_type": strings.TrimSpace(req.Type),
			"runtime_type":  normalizeInstanceRuntimeType(req.RuntimeType),
		},
	})
}

func (s *instanceService) emitInstanceLifecycle(event string, instance *models.Instance, outcome string, refusalCode string) {
	if s == nil || instance == nil {
		return
	}
	emitAudit(s.auditLogger, AuditLogEvent{
		Event:        event,
		InstanceMode: auditModeForExistingInstance(instance),
		InstanceID:   auditIntPtr(instance.ID),
		UserID:       auditIntPtr(instance.UserID),
		Outcome:      outcome,
		RefusalCode:  refusalCode,
		Context:      auditInstanceContext(instance),
	})
}

func (s *instanceService) emitInstanceLifecycleFailure(event string, instance *models.Instance, err error) {
	if s == nil || instance == nil {
		return
	}
	context := auditInstanceContext(instance)
	if code := refusalCodeForError(err); code != "" {
		context["error_code"] = code
	}
	emitAudit(s.auditLogger, AuditLogEvent{
		Event:        event,
		InstanceMode: auditModeForExistingInstance(instance),
		InstanceID:   auditIntPtr(instance.ID),
		UserID:       auditIntPtr(instance.UserID),
		Outcome:      AuditOutcomeFailed,
		Context:      context,
	})
}

func (s *instanceService) emitInstanceRefused(event string, instance *models.Instance, refusalCode string, context map[string]interface{}) {
	if s == nil {
		return
	}
	if context == nil {
		context = map[string]interface{}{}
	}
	if instance == nil {
		emitAudit(s.auditLogger, AuditLogEvent{
			Event:        event,
			InstanceMode: "unknown",
			InstanceID:   nil,
			UserID:       nil,
			Outcome:      AuditOutcomeRefused,
			RefusalCode:  refusalCode,
			Context:      context,
		})
		return
	}
	for key, value := range auditInstanceContext(instance) {
		context[key] = value
	}
	emitAudit(s.auditLogger, AuditLogEvent{
		Event:        event,
		InstanceMode: auditModeForExistingInstance(instance),
		InstanceID:   auditIntPtr(instance.ID),
		UserID:       auditIntPtr(instance.UserID),
		Outcome:      AuditOutcomeRefused,
		RefusalCode:  refusalCode,
		Context:      context,
	})
}

func emitCredentialMinted(logger AuditLogger, instance *models.Instance, credentialType string) {
	if instance == nil {
		return
	}
	emitAudit(logger, AuditLogEvent{
		Event:        AuditEventCredentialMinted,
		InstanceMode: auditModeForExistingInstance(instance),
		InstanceID:   auditIntPtr(instance.ID),
		UserID:       auditIntPtr(instance.UserID),
		Context: map[string]interface{}{
			"credential_type": credentialType,
			"runtime_type":    strings.TrimSpace(instance.RuntimeType),
			"instance_type":   strings.TrimSpace(instance.Type),
		},
	})
}

func emitAudit(logger AuditLogger, event AuditLogEvent) {
	if logger == nil {
		return
	}
	_ = logger.Emit(event)
}

func auditInstanceContext(instance *models.Instance) map[string]interface{} {
	if instance == nil {
		return map[string]interface{}{}
	}
	return map[string]interface{}{
		"instance_name": strings.TrimSpace(instance.Name),
		"instance_type": strings.TrimSpace(instance.Type),
		"runtime_type":  strings.TrimSpace(instance.RuntimeType),
		"status":        strings.TrimSpace(instance.Status),
	}
}

func normalizeAuditInstanceMode(mode string) string {
	if normalized, ok := NormalizeInstanceMode(mode); ok {
		return normalized
	}
	return "unknown"
}

func auditCreateInstanceMode(req CreateInstanceRequest) string {
	if mode, err := resolveCreateInstanceMode(req); err == nil {
		return mode
	}
	if mode := strings.TrimSpace(req.Mode); mode != "" {
		return mode
	}
	return strings.TrimSpace(req.InstanceMode)
}

func auditModeForExistingInstance(instance *models.Instance) string {
	mode, err := modeForExistingInstance(instance)
	if err != nil {
		return "unknown"
	}
	return mode
}

func refusalCodeForError(err error) string {
	if err == nil {
		return ""
	}
	text := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case strings.Contains(text, "egress_proxy_unreachable"):
		return "egress_proxy_unreachable"
	case strings.Contains(text, "capability"):
		return "capability_gate"
	case strings.Contains(text, "already running"):
		return "already_running"
	case strings.Contains(text, "not running"):
		return "invalid_state"
	case strings.Contains(text, "not found"):
		return "not_found"
	case strings.Contains(text, "instance limit reached"):
		return "quota_instance_limit"
	case strings.Contains(text, "cpu cores exceed quota"):
		return "quota_cpu_exceeded"
	case strings.Contains(text, "memory exceed quota"):
		return "quota_memory_exceeded"
	case strings.Contains(text, "storage exceed quota"):
		return "quota_storage_exceeded"
	case strings.Contains(text, "gpu count exceed quota"):
		return "quota_gpu_exceeded"
	case strings.Contains(text, "capacity reached"):
		return "capacity_reached"
	case strings.Contains(text, "mode is disabled"):
		return "mode_disabled"
	case strings.Contains(text, "invalid"), strings.Contains(text, "validation"):
		return "validation_failed"
	default:
		return "backend_error"
	}
}
