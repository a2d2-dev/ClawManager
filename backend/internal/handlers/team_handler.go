package handlers

import (
	"io"
	"net/http"
	"strconv"

	"clawreef/internal/services"
	"clawreef/internal/utils"

	"github.com/gin-gonic/gin"
)

type TeamHandler struct {
	teamService services.TeamService
}

func NewTeamHandler(teamService services.TeamService) *TeamHandler {
	return &TeamHandler{teamService: teamService}
}

func (h *TeamHandler) CreateTeam(c *gin.Context) {
	userID, _ := c.Get("userID")
	var req services.CreateTeamRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		utils.ValidationError(c, err)
		return
	}
	team, err := h.teamService.CreateTeam(userID.(int), req)
	if err != nil {
		utils.HandleError(c, err)
		return
	}
	utils.Success(c, http.StatusCreated, "Team created successfully", team)
}

func (h *TeamHandler) ListTeams(c *gin.Context) {
	userID, _ := c.Get("userID")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if page <= 0 {
		page = 1
	}
	if limit <= 0 {
		limit = 20
	}
	teams, err := h.teamService.ListTeams(userID.(int), (page-1)*limit, limit)
	if err != nil {
		utils.HandleError(c, err)
		return
	}
	utils.Success(c, http.StatusOK, "Teams retrieved successfully", gin.H{
		"teams": teams.Teams,
		"total": teams.Total,
		"page":  page,
		"limit": limit,
	})
}

func (h *TeamHandler) GetTeam(c *gin.Context) {
	userID, _ := c.Get("userID")
	teamID, ok := parseTeamID(c)
	if !ok {
		return
	}
	team, err := h.teamService.GetTeam(userID.(int), teamID)
	if err != nil {
		utils.HandleError(c, err)
		return
	}
	utils.Success(c, http.StatusOK, "Team retrieved successfully", team)
}

func (h *TeamHandler) ListTasks(c *gin.Context) {
	userID, _ := c.Get("userID")
	teamID, ok := parseTeamID(c)
	if !ok {
		return
	}
	tasks, err := h.teamService.ListTeamTasks(
		userID.(int),
		teamID,
		parseOptionalIntQuery(c, "before_id"),
		parseOptionalIntQuery(c, "limit"),
	)
	if err != nil {
		utils.HandleError(c, err)
		return
	}
	utils.Success(c, http.StatusOK, "Team tasks retrieved successfully", tasks)
}

func (h *TeamHandler) ListEvents(c *gin.Context) {
	userID, _ := c.Get("userID")
	teamID, ok := parseTeamID(c)
	if !ok {
		return
	}
	events, err := h.teamService.ListTeamEvents(
		userID.(int),
		teamID,
		parseOptionalIntQuery(c, "before_id"),
		parseOptionalIntQuery(c, "limit"),
	)
	if err != nil {
		utils.HandleError(c, err)
		return
	}
	utils.Success(c, http.StatusOK, "Team events retrieved successfully", events)
}

func (h *TeamHandler) DispatchTask(c *gin.Context) {
	userID, _ := c.Get("userID")
	teamID, ok := parseTeamID(c)
	if !ok {
		return
	}

	var req services.DispatchTeamTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil && err != io.EOF {
		utils.ValidationError(c, err)
		return
	}
	task, err := h.teamService.DispatchTask(userID.(int), teamID, req)
	if err != nil {
		utils.HandleError(c, err)
		return
	}
	utils.Success(c, http.StatusCreated, "Team task dispatched successfully", task)
}

func (h *TeamHandler) DeleteTeam(c *gin.Context) {
	userID, _ := c.Get("userID")
	teamID, ok := parseTeamID(c)
	if !ok {
		return
	}
	if err := h.teamService.DeleteTeam(userID.(int), teamID); err != nil {
		utils.HandleError(c, err)
		return
	}
	utils.Success(c, http.StatusOK, "Team deleted successfully", gin.H{"id": teamID})
}

func (h *TeamHandler) DeleteMember(c *gin.Context) {
	userID, _ := c.Get("userID")
	teamID, ok := parseTeamID(c)
	if !ok {
		return
	}
	memberID := c.Param("memberID")
	if err := h.teamService.DeleteMember(userID.(int), teamID, memberID); err != nil {
		utils.HandleError(c, err)
		return
	}
	utils.Success(c, http.StatusOK, "Team member deleted successfully", gin.H{"member_id": memberID})
}

func parseTeamID(c *gin.Context) (int, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		utils.Error(c, http.StatusBadRequest, "Invalid Team ID")
		return 0, false
	}
	return id, true
}

func parseOptionalIntQuery(c *gin.Context, key string) int {
	raw := c.Query(key)
	if raw == "" {
		return 0
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0
	}
	return value
}
