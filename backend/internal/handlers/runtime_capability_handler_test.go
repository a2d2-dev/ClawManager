package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"clawreef/internal/services"

	"github.com/gin-gonic/gin"
)

func TestRuntimeCapabilityHandlerExposesInstanceModes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewRuntimeCapabilityHandler(services.NewRuntimeCapabilityService(services.RuntimeCapabilities{
		InstanceModes: map[string]services.RuntimeModeCapability{
			services.InstanceModeIsolated: {
				Available: false,
				Reason:    "mode unavailable: agent-sandbox Sandbox CRD sandboxes.agents.x-k8s.io is not installed",
			},
		},
	}))
	router := gin.New()
	router.GET("/api/v1/runtime-capabilities", handler.Get)

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/runtime-capabilities", nil)
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	body := recorder.Body.String()
	for _, want := range []string{`"instance_modes"`, `"lite"`, `"isolated"`, `"pro"`, "sandboxes.agents.x-k8s.io"} {
		if !strings.Contains(body, want) {
			t.Fatalf("runtime capabilities response missing %q in body:\n%s", want, body)
		}
	}
}
