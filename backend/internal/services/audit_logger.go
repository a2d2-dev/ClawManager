package services

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	AuditEventInstanceCreateRefused   = "instance.create.refused"
	AuditEventInstanceCreate          = "instance.create"
	AuditEventInstanceStartRefused    = "instance.start.refused"
	AuditEventInstanceStart           = "instance.start"
	AuditEventInstanceStopRefused     = "instance.stop.refused"
	AuditEventInstanceStop            = "instance.stop"
	AuditEventInstanceDeleteRefused   = "instance.delete.refused"
	AuditEventInstanceDelete          = "instance.delete"
	AuditEventCredentialMinted        = "credential.minted"
	AuditEventAgentRegistered         = "agent.registered"
	AuditEventAgentCommandStarted     = "agent.command.started"
	AuditEventAgentCommandCompleted   = "agent.command.completed"
	AuditEventSkillInstallRequested   = "skill.install.requested"
	AuditEventSkillUninstallRequested = "skill.uninstall.requested"
	AuditEventSandboxReady            = "sandbox.ready"
	AuditEventSandboxFinished         = "sandbox.finished"
	AuditEventSandboxRecreated        = "sandbox.recreated"

	AuditOutcomeSuccess = "success"
	AuditOutcomeRefused = "refused"
	AuditOutcomeFailed  = "failed"
)

const auditLogEnabledEnv = "CLAWMANAGER_AUDIT_LOG_ENABLED"

type AuditLogger interface {
	Emit(event AuditLogEvent) error
}

type AuditLogEvent struct {
	Timestamp    time.Time              `json:"ts"`
	Event        string                 `json:"event"`
	TraceID      *string                `json:"trace_id,omitempty"`
	InstanceMode string                 `json:"instance_mode"`
	InstanceID   *int                   `json:"instance_id"`
	UserID       *int                   `json:"user_id"`
	Outcome      string                 `json:"outcome,omitempty"`
	RefusalCode  string                 `json:"refusal_code,omitempty"`
	Context      map[string]interface{} `json:"-"`
}

type auditLogLine map[string]interface{}

type jsonlAuditLogger struct {
	enabled bool
	sink    io.Writer
	now     func() time.Time
	mu      sync.Mutex
}

type noopAuditLogger struct{}

func NewAuditLoggerFromEnv() AuditLogger {
	return NewJSONLAuditLogger(os.Stdout, auditLogEnvEnabled(os.Getenv(auditLogEnabledEnv)))
}

func NewJSONLAuditLogger(sink io.Writer, enabled bool) AuditLogger {
	if sink == nil {
		sink = io.Discard
	}
	return &jsonlAuditLogger{
		enabled: enabled,
		sink:    sink,
		now:     func() time.Time { return time.Now().UTC() },
	}
}

func NewNoopAuditLogger() AuditLogger {
	return noopAuditLogger{}
}

func auditLogEnvEnabled(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "false", "0", "off", "no":
		return false
	default:
		return true
	}
}

func (l *jsonlAuditLogger) Emit(event AuditLogEvent) error {
	if l == nil || !l.enabled {
		return nil
	}
	if event.Timestamp.IsZero() {
		if l.now != nil {
			event.Timestamp = l.now()
		} else {
			event.Timestamp = time.Now().UTC()
		}
	}
	line := auditLogLine{
		"ts":            event.Timestamp.Format(time.RFC3339Nano),
		"event":         strings.TrimSpace(event.Event),
		"instance_mode": strings.TrimSpace(event.InstanceMode),
		"instance_id":   event.InstanceID,
		"user_id":       event.UserID,
	}
	if event.TraceID != nil && strings.TrimSpace(*event.TraceID) != "" {
		line["trace_id"] = strings.TrimSpace(*event.TraceID)
	}
	if strings.TrimSpace(event.Outcome) != "" {
		line["outcome"] = strings.TrimSpace(event.Outcome)
	}
	if strings.TrimSpace(event.RefusalCode) != "" {
		line["refusal_code"] = strings.TrimSpace(event.RefusalCode)
	}
	for key, value := range event.Context {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, reserved := line[key]; reserved {
			continue
		}
		line[key] = value
	}

	data, err := json.Marshal(line)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	l.mu.Lock()
	defer l.mu.Unlock()
	_, err = l.sink.Write(data)
	return err
}

func (noopAuditLogger) Emit(event AuditLogEvent) error {
	return nil
}

func auditIntPtr(value int) *int {
	return &value
}

func auditStringPtr(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}
