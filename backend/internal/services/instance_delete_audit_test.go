package services

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"clawreef/internal/models"
)

func TestInstanceServiceDeleteProEmitsNoSuccessUntilCompleteDeletionFinishes(t *testing.T) {
	previousCleaner := newInstanceResourceCleaner
	cleaner := &blockingInstanceResourceCleaner{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	newInstanceResourceCleaner = func() instanceResourceCleaner { return cleaner }
	t.Cleanup(func() {
		newInstanceResourceCleaner = previousCleaner
	})

	instanceRepo := newV2LifecycleInstanceRepo()
	instanceRepo.byID[301] = &models.Instance{
		ID:           301,
		UserID:       45,
		Name:         "pro-delete",
		Type:         "ubuntu",
		RuntimeType:  RuntimeBackendDesktop,
		InstanceMode: InstanceModePro,
		Status:       "running",
	}
	auditLogger := newRecordingAuditLogger()
	service := &instanceService{
		instanceRepo: instanceRepo,
		auditLogger:  auditLogger,
	}

	if err := service.Delete(301); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}

	select {
	case <-cleaner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("completeDeletion did not start cleanup")
	}
	if auditLogger.HasEvent(AuditEventInstanceDelete, AuditOutcomeSuccess) {
		t.Fatalf("pro async delete emitted success before completeDeletion finished: %#v", auditLogger.Events())
	}

	close(cleaner.release)
	event := auditLogger.WaitForEvent(t, AuditEventInstanceDelete, AuditOutcomeSuccess)
	if event.InstanceID == nil || *event.InstanceID != 301 {
		t.Fatalf("success audit instance_id = %v, want 301", event.InstanceID)
	}
	if instanceRepo.byID[301] != nil {
		t.Fatal("instance record still exists after successful completeDeletion")
	}
}

func TestProCompleteDeletionFailureEmitsFailedAuditWithErrorCode(t *testing.T) {
	previousCleaner := newInstanceResourceCleaner
	newInstanceResourceCleaner = func() instanceResourceCleaner { return stubInstanceResourceCleaner{} }
	t.Cleanup(func() {
		newInstanceResourceCleaner = previousCleaner
	})

	instanceRepo := newV2LifecycleInstanceRepo()
	instanceRepo.deleteErr = errors.New("delete store unavailable")
	instance := &models.Instance{
		ID:           302,
		UserID:       45,
		Name:         "pro-delete-fails",
		Type:         "ubuntu",
		RuntimeType:  RuntimeBackendDesktop,
		InstanceMode: InstanceModePro,
		Status:       "deleting",
	}
	instanceRepo.byID[302] = instance
	auditLogger := newRecordingAuditLogger()
	service := &instanceService{
		instanceRepo: instanceRepo,
		auditLogger:  auditLogger,
	}

	newProBackend(service).completeDeletion(instance)

	event := auditLogger.WaitForEvent(t, AuditEventInstanceDelete, AuditOutcomeFailed)
	if event.Context["error_code"] != "backend_error" {
		t.Fatalf("error_code = %v, want backend_error", event.Context["error_code"])
	}
	if _, ok := event.Context["error"]; ok {
		t.Fatalf("unexpected raw error context in audit event: %#v", event.Context["error"])
	}
	var line bytes.Buffer
	if err := NewJSONLAuditLogger(&line, true).Emit(event); err != nil {
		t.Fatalf("emit audit JSONL: %v", err)
	}
	if strings.Contains(line.String(), "delete store unavailable") {
		t.Fatalf("audit JSONL line leaked raw error text: %s", line.String())
	}
	if auditLogger.HasEvent(AuditEventInstanceDelete, AuditOutcomeSuccess) {
		t.Fatalf("failed completeDeletion also emitted success: %#v", auditLogger.Events())
	}
}

func TestInstanceServiceDeleteLiteEmitsSuccessAfterSynchronousDeletion(t *testing.T) {
	instanceRepo := newV2LifecycleInstanceRepo()
	auditLogger := newRecordingAuditLogger()
	instanceRepo.beforeDelete = func(id int) {
		if auditLogger.HasEvent(AuditEventInstanceDelete, AuditOutcomeSuccess) {
			t.Fatalf("lite delete emitted success before repository Delete completed")
		}
	}
	instanceRepo.byID[303] = &models.Instance{
		ID:           303,
		UserID:       45,
		Name:         "lite-delete",
		Type:         RuntimeTypeOpenClaw,
		RuntimeType:  RuntimeBackendGateway,
		InstanceMode: InstanceModeLite,
		Status:       "stopped",
	}
	service := &instanceService{
		instanceRepo: instanceRepo,
		bindingRepo:  newFakeRuntimeBindingRepo(),
		agentClient:  &fakeRuntimeAgentClient{},
		auditLogger:  auditLogger,
	}

	if err := service.Delete(303); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}

	event := auditLogger.WaitForEvent(t, AuditEventInstanceDelete, AuditOutcomeSuccess)
	if event.InstanceID == nil || *event.InstanceID != 303 {
		t.Fatalf("success audit instance_id = %v, want 303", event.InstanceID)
	}
	if instanceRepo.byID[303] != nil {
		t.Fatal("lite instance record still exists after Delete returned")
	}
}

type stubInstanceResourceCleaner struct {
	err error
}

func (c stubInstanceResourceCleaner) DeleteAllInstanceResources(context.Context, int, int) error {
	return c.err
}

type blockingInstanceResourceCleaner struct {
	started chan struct{}
	release chan struct{}
	err     error
	once    sync.Once
}

func (c *blockingInstanceResourceCleaner) DeleteAllInstanceResources(context.Context, int, int) error {
	c.once.Do(func() {
		close(c.started)
	})
	<-c.release
	return c.err
}

type recordingAuditLogger struct {
	mu      sync.Mutex
	events  []AuditLogEvent
	emitted chan struct{}
}

func newRecordingAuditLogger() *recordingAuditLogger {
	return &recordingAuditLogger{emitted: make(chan struct{}, 128)}
}

func (l *recordingAuditLogger) Emit(event AuditLogEvent) error {
	l.mu.Lock()
	l.events = append(l.events, event)
	l.mu.Unlock()
	select {
	case l.emitted <- struct{}{}:
	default:
	}
	return nil
}

func (l *recordingAuditLogger) Events() []AuditLogEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	events := make([]AuditLogEvent, len(l.events))
	copy(events, l.events)
	return events
}

func (l *recordingAuditLogger) HasEvent(eventName, outcome string) bool {
	_, ok := l.findEvent(eventName, outcome)
	return ok
}

func (l *recordingAuditLogger) WaitForEvent(t *testing.T, eventName, outcome string) AuditLogEvent {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		if event, ok := l.findEvent(eventName, outcome); ok {
			return event
		}
		select {
		case <-l.emitted:
		case <-deadline:
			t.Fatalf("timed out waiting for audit event %s outcome %s; events=%#v", eventName, outcome, l.Events())
		}
	}
}

func (l *recordingAuditLogger) findEvent(eventName, outcome string) (AuditLogEvent, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, event := range l.events {
		if event.Event == eventName && event.Outcome == outcome {
			return event, true
		}
	}
	return AuditLogEvent{}, false
}
