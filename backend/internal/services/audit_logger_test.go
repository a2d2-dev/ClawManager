package services

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"clawreef/internal/models"
)

func TestJSONLAuditLoggerDisabledEmitsNoLines(t *testing.T) {
	var sink bytes.Buffer
	logger := NewJSONLAuditLogger(&sink, false)

	if err := logger.Emit(AuditLogEvent{
		Event:        AuditEventInstanceCreate,
		InstanceMode: InstanceModeLite,
		InstanceID:   auditIntPtr(123),
		UserID:       auditIntPtr(45),
	}); err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}

	if sink.Len() != 0 {
		t.Fatalf("expected disabled audit logger to emit zero bytes, got %q", sink.String())
	}
}

func TestJSONLAuditLoggerEmitsEventSequenceThroughSink(t *testing.T) {
	var sink bytes.Buffer
	logger := NewJSONLAuditLogger(&sink, true)

	for _, eventName := range []string{AuditEventAgentCommandStarted, AuditEventAgentCommandCompleted} {
		if err := logger.Emit(AuditLogEvent{
			Event:        eventName,
			InstanceMode: InstanceModeLite,
			InstanceID:   auditIntPtr(321),
			UserID:       auditIntPtr(45),
			Context: map[string]interface{}{
				"command_id":   9,
				"command_type": InstanceCommandTypeHealthCheck,
			},
		}); err != nil {
			t.Fatalf("Emit returned error: %v", err)
		}
	}

	lines := strings.Split(strings.TrimSpace(sink.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 audit lines, got %d: %q", len(lines), sink.String())
	}
	t.Log(lines[0])
	t.Log(lines[1])

	var first map[string]interface{}
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("first audit line is not valid JSON: %v", err)
	}
	var second map[string]interface{}
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatalf("second audit line is not valid JSON: %v", err)
	}
	if first["event"] != AuditEventAgentCommandStarted {
		t.Fatalf("first event = %v, want %s", first["event"], AuditEventAgentCommandStarted)
	}
	if second["event"] != AuditEventAgentCommandCompleted {
		t.Fatalf("second event = %v, want %s", second["event"], AuditEventAgentCommandCompleted)
	}
}

func TestInstanceCreateRefusedAuditLineHasNullInstanceID(t *testing.T) {
	var sink bytes.Buffer
	logger := NewJSONLAuditLogger(&sink, true)
	service := NewInstanceService(
		newFakeRuntimeInstanceRepo(),
		&fakeAuditQuotaRepo{quota: &models.UserQuota{
			UserID:       7,
			MaxInstances: 0,
			MaxCPUCores:  8,
			MaxMemoryGB:  16,
			MaxStorageGB: 200,
			MaxGPUCount:  1,
		}},
		&stubLLMModelRepository{},
		nil,
		WithInstanceAuditLogger(logger),
	)

	_, err := service.Create(7, CreateInstanceRequest{
		Name:        "blocked-create",
		Type:        "openclaw",
		CPUCores:    1,
		MemoryGB:    2,
		DiskGB:      20,
		OSType:      "linux",
		OSVersion:   "latest",
		RuntimeType: RuntimeBackendGateway,
	})
	if err == nil {
		t.Fatal("expected create to be refused by instance quota")
	}

	line := strings.TrimSpace(sink.String())
	if line == "" {
		t.Fatal("expected create.refused audit line")
	}
	t.Log(line)

	var got map[string]interface{}
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("audit line is not valid JSON: %v", err)
	}
	if got["event"] != AuditEventInstanceCreateRefused {
		t.Fatalf("event = %v, want %s", got["event"], AuditEventInstanceCreateRefused)
	}
	if got["instance_id"] != nil {
		t.Fatalf("instance_id = %#v, want null", got["instance_id"])
	}
	if got["instance_mode"] != InstanceModeLite {
		t.Fatalf("instance_mode = %v, want %s", got["instance_mode"], InstanceModeLite)
	}
	if got["user_id"] != float64(7) {
		t.Fatalf("user_id = %v, want 7", got["user_id"])
	}
	if got["outcome"] != AuditOutcomeRefused {
		t.Fatalf("outcome = %v, want %s", got["outcome"], AuditOutcomeRefused)
	}
	if got["refusal_code"] != "quota_instance_limit" {
		t.Fatalf("refusal_code = %v, want quota_instance_limit", got["refusal_code"])
	}
}

type fakeAuditQuotaRepo struct {
	quota *models.UserQuota
}

func (r *fakeAuditQuotaRepo) Create(quota *models.UserQuota) error {
	r.quota = quota
	return nil
}

func (r *fakeAuditQuotaRepo) GetByUserID(userID int) (*models.UserQuota, error) {
	return r.quota, nil
}

func (r *fakeAuditQuotaRepo) Update(quota *models.UserQuota) error {
	r.quota = quota
	return nil
}

func (r *fakeAuditQuotaRepo) DeleteByUserID(userID int) error {
	r.quota = nil
	return nil
}

func (r *fakeAuditQuotaRepo) CreateDefaultQuota(userID int) (*models.UserQuota, error) {
	now := time.Now().UTC()
	r.quota = &models.UserQuota{
		UserID:       userID,
		MaxInstances: 10,
		MaxCPUCores:  40,
		MaxMemoryGB:  100,
		MaxStorageGB: 500,
		MaxGPUCount:  2,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	return r.quota, nil
}
