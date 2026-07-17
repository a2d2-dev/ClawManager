package handlers

import (
	"net/http"

	"clawreef/internal/services"
	"clawreef/internal/utils"

	"github.com/gin-gonic/gin"
)

type RuntimeCapabilityHandler struct {
	service services.RuntimeCapabilityService
}

func NewRuntimeCapabilityHandler(service services.RuntimeCapabilityService) *RuntimeCapabilityHandler {
	return &RuntimeCapabilityHandler{service: service}
}

func (h *RuntimeCapabilityHandler) Get(c *gin.Context) {
	if h == nil || h.service == nil {
		utils.Success(c, http.StatusOK, "Runtime capabilities retrieved successfully", services.RuntimeCapabilities{})
		return
	}
	utils.Success(c, http.StatusOK, "Runtime capabilities retrieved successfully", h.service.GetRuntimeCapabilities(c.Request.Context()))
}
