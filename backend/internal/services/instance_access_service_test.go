package services

import (
	"testing"
	"time"
)

func TestInstanceAccessServiceValidatesTokenAcrossServiceInstances(t *testing.T) {
	t.Setenv("INSTANCE_ACCESS_TOKEN_SECRET", "cluster-shared-secret")

	issuer := NewInstanceAccessService()
	validator := NewInstanceAccessService()

	wantUpstream := "clawreef-42-demo-svc.clawmanager-user-7.svc.cluster.local:3001"
	token, err := issuer.GenerateToken(7, 42, "openclaw", "/api/v1/instances/42/proxy/", wantUpstream, 3001, 5*time.Minute)
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}

	validated, err := validator.ValidateToken(token.Token)
	if err != nil {
		t.Fatalf("ValidateToken() error = %v", err)
	}

	if validated.InstanceID != 42 {
		t.Fatalf("validated.InstanceID = %d, want 42", validated.InstanceID)
	}
	if validated.UserID != 7 {
		t.Fatalf("validated.UserID = %d, want 7", validated.UserID)
	}
	if validated.InstanceType != "openclaw" {
		t.Fatalf("validated.InstanceType = %q, want openclaw", validated.InstanceType)
	}
	if validated.Upstream != wantUpstream {
		t.Fatalf("validated.Upstream = %q, want %q", validated.Upstream, wantUpstream)
	}
}

func TestInstanceAccessServiceRejectsExpiredSignedToken(t *testing.T) {
	t.Setenv("INSTANCE_ACCESS_TOKEN_SECRET", "cluster-shared-secret")

	service := NewInstanceAccessService()
	token, err := service.GenerateToken(7, 42, "openclaw", "/api/v1/instances/42/proxy/", "", 3001, -time.Second)
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}

	if _, err := service.ValidateToken(token.Token); err == nil || err.Error() != "token expired" {
		t.Fatalf("ValidateToken() error = %v, want token expired", err)
	}
}

func TestInstanceAccessServiceFallsBackToLegacyTokens(t *testing.T) {
	t.Setenv("INSTANCE_ACCESS_TOKEN_SECRET", "cluster-shared-secret")

	service := NewInstanceAccessService()
	service.tokens["legacy-token"] = &AccessToken{
		Token:        "legacy-token",
		InstanceID:   11,
		UserID:       3,
		InstanceType: "ubuntu",
		TargetPort:   3001,
		AccessURL:    "/api/v1/instances/11/proxy/",
		ExpiresAt:    time.Now().Add(time.Minute),
		CreatedAt:    time.Now(),
	}

	validated, err := service.ValidateToken("legacy-token")
	if err != nil {
		t.Fatalf("ValidateToken() error = %v", err)
	}

	if validated.InstanceID != 11 {
		t.Fatalf("validated.InstanceID = %d, want 11", validated.InstanceID)
	}
}

func TestInstanceAccessServiceStopTerminatesCleanup(t *testing.T) {
	t.Setenv("INSTANCE_ACCESS_TOKEN_SECRET", "cluster-shared-secret")

	service := NewInstanceAccessService()

	// Stop should not panic and should be idempotent-safe (only called once).
	service.Stop()

	// After Stop, the service should still be usable for token operations
	// (only the background cleanup goroutine is stopped).
	token, err := service.GenerateToken(1, 1, "openclaw", "/proxy", "", 3001, time.Minute)
	if err != nil {
		t.Fatalf("GenerateToken after Stop() error = %v", err)
	}
	if _, err := service.ValidateToken(token.Token); err != nil {
		t.Fatalf("ValidateToken after Stop() error = %v", err)
	}
}
