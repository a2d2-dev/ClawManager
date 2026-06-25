package services

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"clawreef/internal/models"
	"clawreef/internal/repository"
	"clawreef/internal/services/k8s"
)

const (
	teamSharedMountPath    = "/team"
	teamConfigFileName     = "team.json"
	teamConfigMountDirPath = "/etc/clawmanager/team"
	teamConfigMountPath    = teamConfigMountDirPath + "/" + teamConfigFileName
	teamSharedUID          = 1000
	teamSharedGID          = 1000
	teamSharedUmask        = "0002"
	teamRedisURLSecretKey  = "CLAWMANAGER_TEAM_REDIS_URL"
	teamTokenSecretKey     = "CLAWMANAGER_TEAM_TOKEN"

	defaultTeamTaskStaleTimeout = 30 * time.Minute
	teamTaskStaleSweepInterval  = 30 * time.Second
	teamConsumerScanInterval    = 10 * time.Second

	initialLeaderTaskIntent = "team_bootstrap_introduction"
	teamTaskCompletionTool  = "team_complete_task"
	teamTaskReplyTarget     = "clawmanager"
)

var (
	teamMemberKeyPattern                = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	teamMemberInstanceNameInvalidChars  = regexp.MustCompile(`[^a-z0-9-]+`)
	teamMemberInstanceNameRepeatedDashs = regexp.MustCompile(`-+`)
)

type TeamService interface {
	StartBackground(ctx context.Context)
	StopBackground()
	CreateTeam(userID int, req CreateTeamRequest) (*TeamDetailsPayload, error)
	ListTeams(userID, offset, limit int) (*TeamListPayload, error)
	GetTeam(userID, teamID int) (*TeamDetailsPayload, error)
	ListTeamTasks(userID, teamID, beforeID, limit int) (*TeamTasksHistoryPayload, error)
	ListTeamEvents(userID, teamID, beforeID, limit int) (*TeamEventsHistoryPayload, error)
	DispatchTask(userID, teamID int, req DispatchTeamTaskRequest) (*TeamTaskPayload, error)
	DeleteTeam(userID, teamID int) error
	DeleteMember(userID, teamID int, memberID string) error
}

type CreateTeamRequest struct {
	Name              string                    `json:"name"`
	Description       *string                   `json:"description,omitempty"`
	CommunicationMode string                    `json:"communication_mode,omitempty"`
	RedisURL          string                    `json:"redis_url,omitempty"`
	SharedStorageGB   int                       `json:"shared_storage_gb,omitempty"`
	StorageClass      string                    `json:"storage_class,omitempty"`
	Members           []CreateTeamMemberRequest `json:"members"`
}

type CreateTeamMemberRequest struct {
	MemberID             string              `json:"member_id,omitempty"`
	Name                 string              `json:"name,omitempty"`
	Role                 string              `json:"role"`
	Mode                 string              `json:"mode,omitempty"`
	InstanceMode         string              `json:"instance_mode,omitempty"`
	RuntimeType          string              `json:"runtime_type,omitempty"`
	Description          *string             `json:"description,omitempty"`
	CPUCores             float64             `json:"cpu_cores,omitempty"`
	MemoryGB             int                 `json:"memory_gb,omitempty"`
	DiskGB               int                 `json:"disk_gb,omitempty"`
	GPUEnabled           bool                `json:"gpu_enabled,omitempty"`
	GPUCount             int                 `json:"gpu_count,omitempty"`
	ImageRegistry        *string             `json:"image_registry,omitempty"`
	ImageTag             *string             `json:"image_tag,omitempty"`
	EnvironmentOverrides map[string]string   `json:"environment_overrides,omitempty"`
	OpenClawConfigPlan   *OpenClawConfigPlan `json:"openclaw_config_plan,omitempty"`
	IsLeader             bool                `json:"is_leader,omitempty"`
}

type DispatchTeamTaskRequest struct {
	TargetMemberID string                 `json:"target_member_id"`
	MessageID      string                 `json:"message_id,omitempty"`
	Payload        map[string]interface{} `json:"payload"`
}

type TeamListPayload struct {
	Teams []models.Team `json:"teams"`
	Total int           `json:"total"`
}

type TeamDetailsPayload struct {
	Team           *models.Team        `json:"team"`
	LeaderMemberID string              `json:"leader_member_id,omitempty"`
	Leader         *models.TeamMember  `json:"leader,omitempty"`
	Members        []models.TeamMember `json:"members"`
	Tasks          []TeamTaskPayload   `json:"tasks,omitempty"`
	Events         []TeamEventPayload  `json:"events,omitempty"`
}

type TeamTasksHistoryPayload struct {
	Tasks        []TeamTaskPayload `json:"tasks"`
	HasMore      bool              `json:"has_more"`
	NextBeforeID *int              `json:"next_before_id,omitempty"`
}

type TeamEventsHistoryPayload struct {
	Events       []TeamEventPayload `json:"events"`
	HasMore      bool               `json:"has_more"`
	NextBeforeID *int               `json:"next_before_id,omitempty"`
}

type TeamTaskPayload struct {
	models.TeamTask
	Payload map[string]interface{} `json:"payload,omitempty"`
	Result  map[string]interface{} `json:"result,omitempty"`
}

type TeamEventPayload struct {
	models.TeamEvent
	Payload map[string]interface{} `json:"payload,omitempty"`
}

type teamService struct {
	repo             repository.TeamRepository
	instanceService  InstanceService
	pvcService       *k8s.PVCService
	secretService    *k8s.SecretService
	configMapService *k8s.ConfigMapService

	ctx                  context.Context
	cancel               context.CancelFunc
	mu                   sync.Mutex
	running              bool
	wg                   sync.WaitGroup
	consumers            map[int]struct{}
	staleMonitorStarted  bool
	runtimeWorkspaceRoot string
}

type plannedTeamMember struct {
	Request      CreateTeamMemberRequest
	MemberKey    string
	DisplayName  string
	Role         string
	RuntimeType  string
	InstanceMode string
	IsLeader     bool
}

type teamRuntimeSecrets struct {
	RedisURL string
	Token    string
}

type TeamServiceOption func(*teamService)

func WithTeamRuntimeWorkspaceRoot(root string) TeamServiceOption {
	return func(s *teamService) {
		if strings.TrimSpace(root) != "" {
			s.runtimeWorkspaceRoot = strings.TrimSpace(root)
		}
	}
}

func NewTeamService(repo repository.TeamRepository, instanceService InstanceService, opts ...TeamServiceOption) TeamService {
	ctx, cancel := context.WithCancel(context.Background())
	service := &teamService{
		repo:                 repo,
		instanceService:      instanceService,
		pvcService:           k8s.NewPVCService(),
		secretService:        k8s.NewSecretService(),
		configMapService:     k8s.NewConfigMapService(),
		ctx:                  ctx,
		cancel:               cancel,
		consumers:            map[int]struct{}{},
		runtimeWorkspaceRoot: "/workspaces",
	}
	for _, opt := range opts {
		if opt != nil {
			opt(service)
		}
	}
	return service
}

// StartBackground starts the leader-only background workers: a periodic scan
// that ensures a Redis event consumer is running for every active team, and
// the stale-task monitor. It is safe to call repeatedly (a second call while
// running is a no-op) and can be called again after StopBackground, which is
// required for leader-election re-acquisition. HTTP request handling does not
// depend on these workers, so followers can still serve the API and the in-pod
// nginx data plane while only the leader runs them.
func (s *teamService) StartBackground(parent context.Context) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	s.ctx = ctx
	s.cancel = cancel
	s.running = true
	s.staleMonitorStarted = false
	s.consumers = map[int]struct{}{}
	s.wg.Add(1)
	go s.consumerScanLoop(ctx)
	s.mu.Unlock()

	fmt.Println("[TeamService] Starting leader-only background workers...")
	s.ensureStaleTaskMonitor(ctx)
}

// StopBackground stops all background workers and blocks until they have fully
// exited, so a subsequent StartBackground starts from a clean state with no
// goroutines from the previous generation still touching shared maps. It is
// idempotent.
func (s *teamService) StopBackground() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	cancel := s.cancel
	s.mu.Unlock()

	fmt.Println("[TeamService] Stopping leader-only background workers...")
	if cancel != nil {
		cancel()
	}
	s.wg.Wait()

	s.mu.Lock()
	s.consumers = map[int]struct{}{}
	s.staleMonitorStarted = false
	s.mu.Unlock()
}

// consumerScanLoop periodically ensures a consumer goroutine exists for every
// active team. Team creation no longer starts consumers inline (that would run
// on whichever replica served the request); the leader picks up newly active
// teams here within teamConsumerScanInterval.
func (s *teamService) consumerScanLoop(ctx context.Context) {
	defer s.wg.Done()

	s.ensureConsumersForActiveTeams(ctx)

	ticker := time.NewTicker(teamConsumerScanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.ensureConsumersForActiveTeams(ctx)
		}
	}
}

func (s *teamService) ensureConsumersForActiveTeams(ctx context.Context) {
	teams, err := s.repo.ListActiveTeams()
	if err != nil {
		fmt.Printf("Warning: failed to list active teams for consumer scan: %v\n", err)
		return
	}
	for i := range teams {
		s.ensureConsumer(ctx, teams[i].ID)
	}
}

func (s *teamService) CreateTeam(userID int, req CreateTeamRequest) (*TeamDetailsPayload, error) {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return nil, fmt.Errorf("team name is required")
	}
	if len(req.Members) == 0 {
		return nil, fmt.Errorf("team must include at least one member")
	}
	memberPlans, err := planTeamMembers(req.Name, req.Members)
	if err != nil {
		return nil, err
	}
	existingTeam, err := s.repo.GetTeamByUserIDAndName(userID, req.Name)
	if err != nil {
		return nil, err
	}
	if existingTeam != nil {
		if existingTeam.Status == models.TeamStatusFailed {
			if err := s.DeleteTeam(userID, existingTeam.ID); err != nil {
				return nil, err
			}
		} else {
			return nil, fmt.Errorf("team name already exists")
		}
	}

	communicationMode := strings.TrimSpace(req.CommunicationMode)
	if communicationMode == "" {
		communicationMode = "leader_mediated"
	}
	redisURL := strings.TrimSpace(req.RedisURL)
	if redisURL == "" {
		redisURL = defaultTeamRedisURL()
	}
	if redisURL == "" {
		return nil, fmt.Errorf("team redis url is required")
	}
	if _, err := newRedisBus(redisURL); err != nil {
		return nil, err
	}

	sharedStorageGB := req.SharedStorageGB
	if sharedStorageGB <= 0 {
		sharedStorageGB = 10
	}
	preflightTeam := &models.Team{
		ID:              0,
		Name:            req.Name,
		StorageClass:    optionalString(strings.TrimSpace(req.StorageClass)),
		SharedMountPath: teamSharedMountPath,
	}
	if err := s.instanceService.ValidateCreateRequests(userID, s.buildTeamMemberInstanceRequests(preflightTeam, memberPlans)); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	storageClass := optionalString(strings.TrimSpace(req.StorageClass))
	team := &models.Team{
		UserID:            userID,
		Name:              req.Name,
		Description:       req.Description,
		Status:            models.TeamStatusCreating,
		CommunicationMode: communicationMode,
		RedisEventsLastID: "0-0",
		SharedMountPath:   teamSharedMountPath,
		StorageClass:      storageClass,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := s.repo.CreateTeam(team); err != nil {
		return nil, err
	}
	if err := s.instanceService.ValidateCreateRequests(userID, s.buildTeamMemberInstanceRequests(team, memberPlans)); err != nil {
		return nil, s.rollbackTeamCreation(userID, team, err)
	}

	runtimeSecrets, err := s.provisionTeamK8s(userID, team, redisURL, sharedStorageGB, strings.TrimSpace(req.StorageClass))
	if err != nil {
		return nil, s.rollbackTeamCreation(userID, team, err)
	}
	rosterJSON, err := s.upsertTeamRosterConfig(userID, team, memberPlans)
	if err != nil {
		return nil, s.rollbackTeamCreation(userID, team, err)
	}

	for _, memberPlan := range memberPlans {
		member, err := s.createTeamMemberInstance(userID, team, memberPlan, runtimeSecrets, rosterJSON)
		if err != nil {
			return nil, s.rollbackTeamCreation(userID, team, err)
		}
		member.Status = models.TeamMemberStatusIdle
		member.UpdatedAt = time.Now().UTC()
		if err := s.repo.UpdateMember(member); err != nil {
			return nil, s.rollbackTeamCreation(userID, team, err)
		}
	}

	team.Status = models.TeamStatusRunning
	team.UpdatedAt = time.Now().UTC()
	if err := s.repo.UpdateTeam(team); err != nil {
		return nil, err
	}
	// Background consumers / stale-task monitor are leader-only and started by
	// the leader's periodic scan (consumerScanLoop). Starting them here would
	// run them on whichever replica served the create request, bypassing
	// leader election, so we intentionally do not call ensureConsumer here.
	if err := s.dispatchInitialLeaderTask(userID, team); err != nil {
		fmt.Printf("Warning: failed to dispatch initial Team %d leader task: %v\n", team.ID, err)
		if recordErr := s.recordInitialLeaderTaskDispatchFailure(team.ID, err); recordErr != nil {
			fmt.Printf("Warning: failed to record Team %d initial leader task dispatch failure: %v\n", team.ID, recordErr)
		}
	}
	return s.GetTeam(userID, team.ID)
}

func (s *teamService) dispatchInitialLeaderTask(userID int, team *models.Team) error {
	if team == nil {
		return fmt.Errorf("team is required")
	}
	members, err := s.repo.ListMembersByTeamID(team.ID)
	if err != nil {
		return err
	}
	leader := findTeamLeader(activeTeamMembers(members))
	if leader == nil {
		return fmt.Errorf("team leader not found")
	}
	_, err = s.DispatchTask(userID, team.ID, DispatchTeamTaskRequest{
		TargetMemberID: leader.MemberKey,
		MessageID:      initialLeaderTaskMessageID(team.ID),
		Payload:        buildInitialLeaderTaskPayload(team.Name),
	})
	return err
}

func initialLeaderTaskMessageID(teamID int) string {
	return fmt.Sprintf("team-%d-bootstrap-introduction", teamID)
}

func (s *teamService) recordInitialLeaderTaskDispatchFailure(teamID int, cause error) error {
	now := time.Now().UTC()
	payload := map[string]interface{}{
		"v":         1,
		"event":     "bootstrap_dispatch_failed",
		"teamId":    strconv.Itoa(teamID),
		"intent":    initialLeaderTaskIntent,
		"messageId": initialLeaderTaskMessageID(teamID),
		"source":    "clawmanager",
	}
	if cause != nil {
		payload["diagnostic"] = cause.Error()
	}
	payloadJSON, err := marshalOptionalJSON(payload)
	if err != nil {
		return err
	}
	messageID := initialLeaderTaskMessageID(teamID)
	return s.repo.CreateEvent(&models.TeamEvent{
		TeamID:      teamID,
		MessageID:   &messageID,
		EventType:   "bootstrap_dispatch_failed",
		PayloadJSON: payloadJSON,
		OccurredAt:  &now,
		CreatedAt:   now,
	})
}

func buildTeamTaskEnvelope(teamID int, memberKey string, task *models.TeamTask, messageID string, taskPayload map[string]interface{}, now time.Time) map[string]interface{} {
	if taskPayload == nil {
		taskPayload = map[string]interface{}{}
	}
	taskID := 0
	if task != nil {
		taskID = task.ID
	}
	taskRef := fmt.Sprintf("team-%d-task-%d", teamID, taskID)
	prompt := eventString(taskPayload, "prompt", "goal", "instruction", "instructions")
	if prompt == "" {
		rawPayload, _ := marshalJSON(taskPayload)
		prompt = rawPayload
	}
	envelope := map[string]interface{}{
		"v":                  1,
		"messageId":          messageID,
		"teamId":             strconv.Itoa(teamID),
		"from":               "clawmanager",
		"to":                 memberKey,
		"replyTo":            teamTaskReplyTarget,
		"requiresCompletion": true,
		"completionTool":     teamTaskCompletionTool,
		"resultSink": map[string]interface{}{
			"type":           "redis_stream",
			"eventsKey":      teamEventsKey(teamID),
			"successEvent":   "task_completed",
			"failureEvent":   "task_failed",
			"replyEvent":     "reply",
			"resultField":    "resultMarkdown",
			"summaryField":   "summary",
			"artifactField":  "artifactRefs",
			"completionTool": teamTaskCompletionTool,
		},
		"intent":      eventString(taskPayload, "intent"),
		"taskId":      taskRef,
		"title":       eventString(taskPayload, "title"),
		"prompt":      appendTeamTaskCompletionInstruction(prompt),
		"contextRefs": normalizeContextRefs(taskPayload["contextRefs"]),
		"metadata":    taskPayload,
		"createdAt":   now.Format(time.RFC3339Nano),
	}
	if envelope["intent"] == "" {
		envelope["intent"] = "run_task"
	}
	if envelope["title"] == "" {
		envelope["title"] = fmt.Sprintf("Team task %d", taskID)
	}
	return envelope
}

func appendTeamTaskCompletionInstruction(prompt string) string {
	base := strings.TrimSpace(prompt)
	if strings.Contains(base, teamTaskCompletionTool) && strings.Contains(base, "task_completed") {
		return base
	}
	instruction := strings.Join([]string{
		"Completion contract:",
		"- When the final result is ready, call team_complete_task with status=\"succeeded\", summary, and resultMarkdown.",
		"- If the task fails, call team_complete_task with status=\"failed\" and an error message.",
		"- Do not send the final answer as a normal message to clawmanager; ClawManager consumes task_completed/task_failed events from the Team Redis event stream.",
	}, "\n")
	if base == "" {
		return instruction
	}
	return base + "\n\n" + instruction
}

func (s *teamService) provisionTeamK8s(userID int, team *models.Team, redisURL string, sharedStorageGB int, storageClass string) (*teamRuntimeSecrets, error) {
	ctx := context.Background()
	pvc, err := s.pvcService.CreateTeamSharedPVC(ctx, userID, team.ID, sharedStorageGB, storageClass)
	if err != nil {
		return nil, err
	}
	secretName := s.pvcService.GetClient().GetTeamSecretName(team.ID)
	teamToken, err := generatePrefixedToken("team")
	if err != nil {
		return nil, fmt.Errorf("failed to generate Team token: %w", err)
	}
	if err := s.secretService.UpsertSecret(ctx, userID, secretName, map[string]string{
		teamRedisURLSecretKey: redisURL,
		teamTokenSecretKey:    teamToken,
	}, map[string]string{
		"app":        "clawreef",
		"managed-by": "clawreef",
		"team-id":    strconv.Itoa(team.ID),
	}); err != nil {
		return nil, err
	}

	team.RedisURLSecretName = &secretName
	team.RedisURLSecretKey = optionalString(teamRedisURLSecretKey)
	team.TeamTokenSecretName = &secretName
	team.TeamTokenSecretKey = optionalString(teamTokenSecretKey)
	team.SharedPVCName = &pvc.Name
	team.SharedPVCNamespace = &pvc.Namespace
	team.UpdatedAt = time.Now().UTC()
	if err := s.repo.UpdateTeam(team); err != nil {
		return nil, err
	}
	return &teamRuntimeSecrets{RedisURL: redisURL, Token: teamToken}, nil
}

func (s *teamService) upsertTeamRosterConfig(userID int, team *models.Team, members []plannedTeamMember) (string, error) {
	rosterJSON, err := buildTeamRosterConfig(team, members)
	if err != nil {
		return "", err
	}
	if err := s.configMapService.UpsertConfigMap(context.Background(), userID, s.teamConfigMapName(team.ID), map[string]string{
		teamConfigFileName: rosterJSON,
	}, map[string]string{
		"app":        "clawreef",
		"managed-by": "clawreef",
		"team-id":    strconv.Itoa(team.ID),
	}); err != nil {
		return "", err
	}
	return rosterJSON, nil
}

func (s *teamService) teamConfigMapName(teamID int) string {
	client := k8s.GetClient()
	if client == nil {
		return fmt.Sprintf("clawreef-team-%d-config", teamID)
	}
	return client.GetTeamConfigMapName(teamID)
}

func (s *teamService) createTeamMemberInstance(userID int, team *models.Team, memberPlan plannedTeamMember, runtimeSecrets *teamRuntimeSecrets, rosterJSON string) (*models.TeamMember, error) {
	now := time.Now().UTC()
	member := &models.TeamMember{
		TeamID:       team.ID,
		UserID:       userID,
		MemberKey:    memberPlan.MemberKey,
		DisplayName:  memberPlan.DisplayName,
		Role:         memberPlan.Role,
		RuntimeType:  memberPlan.RuntimeType,
		InstanceMode: memberPlan.InstanceMode,
		Description:  optionalString(strings.TrimSpace(derefTeamString(memberPlan.Request.Description))),
		Status:       models.TeamMemberStatusCreating,
		Availability: models.TeamMemberAvailabilityUnknown,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.repo.CreateMember(member); err != nil {
		return nil, err
	}

	createReq := s.buildTeamMemberInstanceRequestWithSecrets(team, memberPlan, runtimeSecrets, rosterJSON)
	instance, err := s.instanceService.Create(userID, createReq)
	if err != nil {
		member.Status = models.TeamMemberStatusFailed
		member.UpdatedAt = time.Now().UTC()
		_ = s.repo.UpdateMember(member)
		return nil, err
	}
	member.InstanceID = &instance.ID
	member.UpdatedAt = time.Now().UTC()
	if err := s.repo.UpdateMember(member); err != nil {
		return nil, err
	}
	return member, nil
}

func (s *teamService) buildTeamMemberInstanceRequests(team *models.Team, memberPlans []plannedTeamMember) []CreateInstanceRequest {
	requests := make([]CreateInstanceRequest, 0, len(memberPlans))
	for _, memberPlan := range memberPlans {
		requests = append(requests, s.buildTeamMemberInstanceRequest(team, memberPlan))
	}
	return requests
}

func (s *teamService) buildTeamMemberInstanceRequest(team *models.Team, memberPlan plannedTeamMember) CreateInstanceRequest {
	return s.buildTeamMemberInstanceRequestWithSecrets(team, memberPlan, nil, "")
}

func (s *teamService) buildTeamMemberInstanceRequestWithSecrets(team *models.Team, memberPlan plannedTeamMember, runtimeSecrets *teamRuntimeSecrets, rosterJSON string) CreateInstanceRequest {
	req := memberPlan.Request
	instanceMode := memberPlan.InstanceMode
	if instanceMode == "" {
		instanceMode = InstanceModeLite
	}
	runtimeBackendType, _ := RuntimeTypeForInstanceMode(instanceMode)
	memberEnv := s.teamMemberEnv(team, memberPlan.MemberKey, memberPlan.Role)
	if instanceMode == InstanceModeLite {
		memberEnv["CLAWMANAGER_TEAM_SHARED_DIR"] = s.teamRuntimeSharedPath(team)
	}
	environmentOverrides := mergeEnvMaps(req.EnvironmentOverrides, memberEnv)
	if instanceMode == InstanceModeLite && runtimeSecrets != nil {
		environmentOverrides = mergeEnvMaps(environmentOverrides, map[string]string{
			teamRedisURLSecretKey: runtimeSecrets.RedisURL,
			teamTokenSecretKey:    runtimeSecrets.Token,
		})
		if strings.TrimSpace(rosterJSON) != "" {
			environmentOverrides["CLAWMANAGER_TEAM_CONFIG_JSON"] = rosterJSONWithSharedDir(rosterJSON, s.teamRuntimeSharedPath(team))
		}
	}
	return CreateInstanceRequest{
		Name:                 teamMemberInstanceName(team.Name, team.ID, memberPlan.MemberKey),
		Type:                 memberPlan.RuntimeType,
		Mode:                 instanceMode,
		InstanceMode:         instanceMode,
		RuntimeType:          runtimeBackendType,
		CPUCores:             defaultFloat(req.CPUCores, 2),
		MemoryGB:             defaultInt(req.MemoryGB, 4),
		DiskGB:               defaultInt(req.DiskGB, 20),
		GPUEnabled:           req.GPUEnabled,
		GPUCount:             req.GPUCount,
		OSType:               memberPlan.RuntimeType,
		OSVersion:            "latest",
		ImageRegistry:        req.ImageRegistry,
		ImageTag:             req.ImageTag,
		EnvironmentOverrides: environmentOverrides,
		StorageClass:         derefTeamString(team.StorageClass),
		OpenClawConfigPlan:   req.OpenClawConfigPlan,
		Team: &TeamInstanceConfig{
			Environment:     s.teamMemberEnv(team, memberPlan.MemberKey, memberPlan.Role),
			SecretName:      derefTeamString(team.TeamTokenSecretName),
			SharedPVCName:   derefTeamString(team.SharedPVCName),
			SharedMountPath: team.SharedMountPath,
			ConfigMapName:   s.teamConfigMapName(team.ID),
			ConfigMountPath: teamConfigMountDirPath,
			SharedUID:       teamSharedUID,
			SharedGID:       teamSharedGID,
			SharedUmask:     teamSharedUmask,
		},
	}
}

func (s *teamService) teamRuntimeSharedPath(team *models.Team) string {
	if team == nil {
		return k8s.TeamSharedWorkspacePath(s.runtimeWorkspaceRoot, 0, 0)
	}
	return k8s.TeamSharedWorkspacePath(s.runtimeWorkspaceRoot, team.UserID, team.ID)
}

func (s *teamService) teamMemberEnv(team *models.Team, memberKey, role string) map[string]string {
	managerBaseURL, _ := defaultTeamManagerBaseURL()
	return map[string]string{
		"CLAWMANAGER_TEAM_ENABLED":        "true",
		"CLAWMANAGER_TEAM_ID":             strconv.Itoa(team.ID),
		"CLAWMANAGER_TEAM_MEMBER_ID":      memberKey,
		"CLAWMANAGER_TEAM_ROLE":           role,
		"CLAWMANAGER_TEAM_SHARED_DIR":     team.SharedMountPath,
		"CLAWMANAGER_TEAM_SHARED_UID":     strconv.Itoa(teamSharedUID),
		"CLAWMANAGER_TEAM_SHARED_GID":     strconv.Itoa(teamSharedGID),
		"CLAWMANAGER_TEAM_UMASK":          teamSharedUmask,
		"PUID":                            strconv.Itoa(teamSharedUID),
		"PGID":                            strconv.Itoa(teamSharedGID),
		"UMASK":                           teamSharedUmask,
		"CLAWMANAGER_TEAM_CONFIG_PATH":    teamConfigMountPath,
		"CLAWMANAGER_TEAM_AUTORUN":        "true",
		"CLAWMANAGER_TEAM_CONSUMER_GROUP": "team-members",
		"CLAWMANAGER_TEAM_INBOX_KEY":      teamInboxKey(team.ID, memberKey),
		"CLAWMANAGER_TEAM_EVENTS_KEY":     teamEventsKey(team.ID),
		"CLAWMANAGER_TEAM_PRESENCE_KEY":   teamPresenceKey(team.ID),
		"CLAWMANAGER_TEAM_DLQ_KEY":        teamDLQKey(team.ID),
		"CLAWMANAGER_TEAM_MANAGER_URL":    managerBaseURL,
	}
}

func (s *teamService) ListTeams(userID, offset, limit int) (*TeamListPayload, error) {
	teams, err := s.repo.ListTeamsByUserID(userID, offset, limit)
	if err != nil {
		return nil, err
	}
	teams = activeTeams(teams)
	total, err := s.repo.CountTeamsByUserID(userID)
	if err != nil {
		return nil, err
	}
	return &TeamListPayload{Teams: teams, Total: total}, nil
}

func (s *teamService) GetTeam(userID, teamID int) (*TeamDetailsPayload, error) {
	team, err := s.requireOwnedTeam(userID, teamID)
	if err != nil {
		return nil, err
	}
	members, err := s.repo.ListMembersByTeamID(teamID)
	if err != nil {
		return nil, err
	}
	members = activeTeamMembers(members)
	tasks, err := s.repo.ListTasksByTeamID(teamID, 20)
	if err != nil {
		return nil, err
	}
	events, err := s.repo.ListEventsByTeamID(teamID, 50)
	if err != nil {
		return nil, err
	}
	leader := findTeamLeader(members)
	return &TeamDetailsPayload{
		Team:           team,
		LeaderMemberID: leaderMemberKey(leader),
		Leader:         leader,
		Members:        members,
		Tasks:          teamTaskPayloads(tasks),
		Events:         teamEventPayloads(events),
	}, nil
}

func (s *teamService) ListTeamTasks(userID, teamID, beforeID, limit int) (*TeamTasksHistoryPayload, error) {
	if _, err := s.requireOwnedTeam(userID, teamID); err != nil {
		return nil, err
	}
	limit = normalizeTeamHistoryLimit(limit, 20, 100)
	tasks, err := s.repo.ListTasksBeforeID(teamID, beforeID, limit+1)
	if err != nil {
		return nil, err
	}
	hasMore := len(tasks) > limit
	if hasMore {
		tasks = tasks[:limit]
	}
	payload := teamTaskPayloads(tasks)
	return &TeamTasksHistoryPayload{
		Tasks:        payload,
		HasMore:      hasMore,
		NextBeforeID: nextTeamTaskBeforeID(payload),
	}, nil
}

func (s *teamService) ListTeamEvents(userID, teamID, beforeID, limit int) (*TeamEventsHistoryPayload, error) {
	if _, err := s.requireOwnedTeam(userID, teamID); err != nil {
		return nil, err
	}
	limit = normalizeTeamHistoryLimit(limit, 50, 200)
	events, err := s.repo.ListEventsBeforeID(teamID, beforeID, limit+1)
	if err != nil {
		return nil, err
	}
	hasMore := len(events) > limit
	if hasMore {
		events = events[:limit]
	}
	payload := teamEventPayloads(events)
	return &TeamEventsHistoryPayload{
		Events:       payload,
		HasMore:      hasMore,
		NextBeforeID: nextTeamEventBeforeID(payload),
	}, nil
}

func (s *teamService) DispatchTask(userID, teamID int, req DispatchTeamTaskRequest) (*TeamTaskPayload, error) {
	team, err := s.requireOwnedTeam(userID, teamID)
	if err != nil {
		return nil, err
	}
	memberKey := strings.TrimSpace(req.TargetMemberID)
	if memberKey == "" {
		members, err := s.repo.ListMembersByTeamID(teamID)
		if err != nil {
			return nil, err
		}
		memberKey = leaderMemberKey(findTeamLeader(activeTeamMembers(members)))
	}
	if memberKey == "" {
		return nil, fmt.Errorf("target member id is required")
	}
	if req.Payload == nil {
		return nil, fmt.Errorf("task payload is required")
	}
	member, err := s.repo.GetMemberByTeamKey(teamID, memberKey)
	if err != nil {
		return nil, err
	}
	if member == nil {
		return nil, fmt.Errorf("team member not found")
	}

	messageID := strings.TrimSpace(req.MessageID)
	if messageID == "" {
		messageID = fmt.Sprintf("team-%d-task-%d", teamID, time.Now().UTC().UnixNano())
	}
	existing, err := s.repo.GetTaskByMessageID(teamID, messageID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if existing.TargetMemberID != member.ID {
			return nil, fmt.Errorf("team task message id already exists")
		}
		if existing.Status != models.TeamTaskStatusPending || existing.RedisStreamID != nil {
			return teamTaskPayload(*existing)
		}
	} else {
		payloadJSON, err := marshalJSON(req.Payload)
		if err != nil {
			return nil, fmt.Errorf("failed to encode task payload: %w", err)
		}
		now := time.Now().UTC()
		existing = &models.TeamTask{
			TeamID:         teamID,
			TargetMemberID: member.ID,
			CreatedBy:      &userID,
			MessageID:      messageID,
			Status:         models.TeamTaskStatusPending,
			PayloadJSON:    payloadJSON,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		if err := s.repo.CreateTask(existing); err != nil {
			return nil, err
		}
	}
	task := existing

	bus, err := s.redisBusForTeam(context.Background(), team)
	if err != nil {
		return nil, err
	}
	taskPayload := map[string]interface{}{}
	if strings.TrimSpace(task.PayloadJSON) != "" {
		if err := json.Unmarshal([]byte(task.PayloadJSON), &taskPayload); err != nil {
			return nil, fmt.Errorf("failed to decode task payload: %w", err)
		}
	}
	now := time.Now().UTC()
	envelope := buildTeamTaskEnvelope(teamID, member.MemberKey, task, messageID, taskPayload, now)
	envelopeJSON, err := marshalJSON(envelope)
	if err != nil {
		return nil, fmt.Errorf("failed to encode task envelope: %w", err)
	}
	streamID, err := bus.XAdd(context.Background(), teamInboxKey(team.ID, member.MemberKey), map[string]string{
		"payload":    envelopeJSON,
		"team_id":    strconv.Itoa(team.ID),
		"task_id":    strconv.Itoa(task.ID),
		"message_id": messageID,
		"member_id":  member.MemberKey,
	})
	if err != nil {
		return nil, err
	}
	task.Status = models.TeamTaskStatusDispatched
	task.RedisStreamID = &streamID
	task.DispatchedAt = &now
	task.UpdatedAt = time.Now().UTC()
	if err := s.repo.UpdateTask(task); err != nil {
		return nil, err
	}
	return teamTaskPayload(*task)
}

func (s *teamService) DeleteTeam(userID, teamID int) error {
	team, err := s.requireOwnedTeam(userID, teamID)
	if err != nil {
		return err
	}
	if team.Status == models.TeamStatusDeleted {
		return nil
	}

	now := time.Now().UTC()
	team.Status = models.TeamStatusDeleting
	team.UpdatedAt = now
	if err := s.repo.UpdateTeam(team); err != nil {
		return err
	}

	members, err := s.repo.ListMembersByTeamID(teamID)
	if err != nil {
		return err
	}
	for idx := range members {
		member := members[idx]
		if member.Status == models.TeamMemberStatusDeleted {
			continue
		}
		member.Status = models.TeamMemberStatusDeleting
		member.UpdatedAt = time.Now().UTC()
		_ = s.repo.UpdateMember(&member)
		if member.InstanceID != nil && *member.InstanceID > 0 {
			if err := s.instanceService.Delete(*member.InstanceID); err != nil {
				fmt.Printf("Warning: failed to delete Team %d member %s instance %d: %v\n", teamID, member.MemberKey, *member.InstanceID, err)
			}
		}
		member.Status = models.TeamMemberStatusDeleted
		member.CurrentTaskID = nil
		member.UpdatedAt = time.Now().UTC()
		_ = s.repo.UpdateMember(&member)
	}

	ctx := context.Background()
	if strings.TrimSpace(derefTeamString(team.TeamTokenSecretName)) != "" {
		if err := s.secretService.DeleteSecret(ctx, userID, derefTeamString(team.TeamTokenSecretName)); err != nil {
			fmt.Printf("Warning: failed to delete Team %d secret: %v\n", teamID, err)
		}
	}
	if err := s.configMapService.DeleteConfigMap(ctx, userID, s.teamConfigMapName(teamID)); err != nil {
		fmt.Printf("Warning: failed to delete Team %d configmap: %v\n", teamID, err)
	}
	if err := s.pvcService.DeleteTeamSharedPVC(ctx, userID, teamID); err != nil {
		fmt.Printf("Warning: failed to delete Team %d shared PVC: %v\n", teamID, err)
	}

	team.Name = deletedTeamName(team.Name, team.ID)
	team.Status = models.TeamStatusDeleted
	team.UpdatedAt = time.Now().UTC()
	return s.repo.UpdateTeam(team)
}

func (s *teamService) DeleteMember(userID, teamID int, memberID string) error {
	team, err := s.requireOwnedTeam(userID, teamID)
	if err != nil {
		return err
	}
	member, err := s.findTeamMemberForDelete(teamID, memberID)
	if err != nil {
		return err
	}
	if member == nil {
		return fmt.Errorf("team member not found")
	}
	if member.UserID != userID || member.TeamID != teamID {
		return fmt.Errorf("access denied")
	}
	if member.Status == models.TeamMemberStatusDeleted {
		return nil
	}
	if isTeamLeaderRole(member.Role) {
		return fmt.Errorf("team leader cannot be deleted before assigning a new leader")
	}

	now := time.Now().UTC()
	member.Status = models.TeamMemberStatusDeleting
	member.UpdatedAt = now
	if err := s.repo.UpdateMember(member); err != nil {
		return err
	}
	if member.InstanceID != nil && *member.InstanceID > 0 {
		if err := s.instanceService.Delete(*member.InstanceID); err != nil {
			return err
		}
	}
	member.Status = models.TeamMemberStatusDeleted
	member.CurrentTaskID = nil
	member.Progress = 0
	member.UpdatedAt = time.Now().UTC()
	if err := s.repo.UpdateMember(member); err != nil {
		return err
	}
	return s.refreshTeamRosterConfig(userID, team)
}

func (s *teamService) findTeamMemberForDelete(teamID int, memberID string) (*models.TeamMember, error) {
	value := strings.TrimSpace(memberID)
	if value == "" {
		return nil, fmt.Errorf("team member id is required")
	}
	if numericID, err := strconv.Atoi(value); err == nil && numericID > 0 {
		member, err := s.repo.GetMemberByID(numericID)
		if err != nil || member == nil || member.TeamID != teamID {
			return member, err
		}
		return member, nil
	}
	return s.repo.GetMemberByTeamKey(teamID, value)
}

func (s *teamService) refreshTeamRosterConfig(userID int, team *models.Team) error {
	members, err := s.repo.ListMembersByTeamID(team.ID)
	if err != nil {
		return err
	}
	rosterJSON, err := buildTeamRosterConfigFromMembers(team, activeTeamMembers(members))
	if err != nil {
		return err
	}
	return s.configMapService.UpsertConfigMap(context.Background(), userID, s.teamConfigMapName(team.ID), map[string]string{
		teamConfigFileName: rosterJSON,
	}, map[string]string{
		"app":        "clawreef",
		"managed-by": "clawreef",
		"team-id":    strconv.Itoa(team.ID),
	})
}

func (s *teamService) requireOwnedTeam(userID, teamID int) (*models.Team, error) {
	team, err := s.repo.GetTeamByID(teamID)
	if err != nil {
		return nil, err
	}
	if team == nil {
		return nil, fmt.Errorf("team not found")
	}
	if team.UserID != userID {
		return nil, fmt.Errorf("access denied")
	}
	return team, nil
}

func (s *teamService) ensureConsumer(ctx context.Context, teamID int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return
	}
	if _, exists := s.consumers[teamID]; exists {
		return
	}
	s.consumers[teamID] = struct{}{}
	s.wg.Add(1)
	go s.consumeTeamEvents(ctx, teamID)
}

func (s *teamService) consumeTeamEvents(ctx context.Context, teamID int) {
	defer s.wg.Done()
	defer func() {
		s.mu.Lock()
		delete(s.consumers, teamID)
		s.mu.Unlock()
	}()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		team, err := s.repo.GetTeamByID(teamID)
		if err != nil || team == nil {
			time.Sleep(5 * time.Second)
			continue
		}
		bus, err := s.redisBusForTeam(ctx, team)
		if err != nil {
			time.Sleep(5 * time.Second)
			continue
		}
		lastID := strings.TrimSpace(team.RedisEventsLastID)
		if lastID == "" {
			lastID = "0-0"
		}
		messages, err := bus.XRead(ctx, teamEventsKey(teamID), lastID, 5*time.Second)
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}
		for _, message := range messages {
			if err := s.projectTeamEvent(team, bus, message); err != nil {
				fmt.Printf("Warning: failed to project Team %d event %s: %v\n", teamID, message.ID, err)
			}
			team.RedisEventsLastID = message.ID
			team.UpdatedAt = time.Now().UTC()
			_ = s.repo.UpdateTeam(team)
		}
	}
}

func (s *teamService) ensureStaleTaskMonitor(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return
	}
	if s.staleMonitorStarted {
		return
	}
	s.staleMonitorStarted = true
	s.wg.Add(1)
	go s.monitorStaleTasks(ctx)
}

func (s *teamService) monitorStaleTasks(ctx context.Context) {
	defer s.wg.Done()
	ticker := time.NewTicker(teamTaskStaleSweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.sweepStaleTasks(); err != nil {
				fmt.Printf("Warning: failed to sweep stale Team tasks: %v\n", err)
			}
		}
	}
}

func (s *teamService) sweepStaleTasks() error {
	timeout := teamTaskStaleTimeout()
	if timeout <= 0 {
		return nil
	}
	cutoff := time.Now().UTC().Add(-timeout)
	tasks, err := s.repo.ListStaleCandidateTasks(cutoff, 100)
	if err != nil {
		return err
	}
	for idx := range tasks {
		if err := s.markTaskStale(&tasks[idx], timeout); err != nil {
			fmt.Printf("Warning: failed to mark Team task %d stale: %v\n", tasks[idx].ID, err)
		}
	}
	return nil
}

func (s *teamService) markTaskStale(task *models.TeamTask, timeout time.Duration) error {
	if task == nil {
		return nil
	}
	if task.Status != models.TeamTaskStatusDispatched && task.Status != models.TeamTaskStatusRunning {
		return nil
	}
	lastUpdatedAt := task.UpdatedAt
	team, err := s.repo.GetTeamByID(task.TeamID)
	if err != nil {
		return err
	}
	if team == nil || team.Status == models.TeamStatusDeleted || team.Status == models.TeamStatusDeleting {
		return nil
	}

	now := time.Now().UTC()
	previousStatus := task.Status
	task.Status = models.TeamTaskStatusStale
	task.FinishedAt = &now
	message := fmt.Sprintf("Team task stale: no runtime event for %s since %s", timeout.String(), task.UpdatedAt.Format(time.RFC3339))
	task.ErrorMessage = &message
	task.UpdatedAt = now
	if err := s.repo.UpdateTask(task); err != nil {
		return err
	}

	member, err := s.repo.GetMemberByID(task.TargetMemberID)
	if err != nil {
		return err
	}
	if member != nil && member.TeamID == task.TeamID && member.CurrentTaskID != nil && *member.CurrentTaskID == task.ID {
		member.Status = models.TeamMemberStatusIdle
		member.CurrentTaskID = nil
		member.Availability = models.TeamMemberAvailabilityBlocked
		member.RuntimeTaskID = &task.MessageID
		member.RuntimeIntent = nil
		member.BlockedReason = &message
		member.LastSummary = &message
		member.Progress = 0
		member.UpdatedAt = now
		if err := s.repo.UpdateMember(member); err != nil {
			return err
		}
	}

	payload := map[string]interface{}{
		"v":                 1,
		"event":             "task_stale",
		"teamId":            strconv.Itoa(task.TeamID),
		"taskId":            fmt.Sprintf("team-%d-task-%d", task.TeamID, task.ID),
		"messageId":         task.MessageID,
		"previousStatus":    previousStatus,
		"staleAfterSeconds": int(timeout.Seconds()),
		"lastTaskUpdatedAt": lastUpdatedAt.Format(time.RFC3339Nano),
		"diagnostic":        message,
		"source":            "clawmanager",
	}
	payloadJSON, err := marshalOptionalJSON(payload)
	if err != nil {
		return err
	}
	event := &models.TeamEvent{
		TeamID:      task.TeamID,
		TaskID:      &task.ID,
		EventType:   "task_stale",
		MessageID:   &task.MessageID,
		PayloadJSON: payloadJSON,
		OccurredAt:  &now,
		CreatedAt:   now,
	}
	if member != nil && member.TeamID == task.TeamID {
		event.MemberID = &member.ID
	}
	return s.repo.CreateEvent(event)
}

func (s *teamService) redisBusForTeam(ctx context.Context, team *models.Team) (*redisBus, error) {
	redisURL := ""
	if team.RedisURLSecretName != nil && team.RedisURLSecretKey != nil {
		client := k8s.GetClient()
		if client == nil {
			return nil, fmt.Errorf("k8s client not initialized")
		}
		value, err := s.secretService.GetSecretValue(ctx, client.GetNamespace(team.UserID), *team.RedisURLSecretName, *team.RedisURLSecretKey)
		if err != nil {
			return nil, err
		}
		redisURL = strings.TrimSpace(value)
	}
	if redisURL == "" {
		redisURL = defaultTeamRedisURL()
	}
	if redisURL == "" {
		return nil, fmt.Errorf("team redis url is required")
	}
	return newRedisBus(redisURL)
}

func (s *teamService) projectTeamEvent(team *models.Team, bus *redisBus, message redisStreamMessage) error {
	if exists, err := s.repo.EventExistsByStreamID(team.ID, message.ID); err != nil || exists {
		return err
	}
	payload := mergeRedisEventPayload(message.Fields)
	eventType := eventString(payload, "event_type", "event", "type")
	if eventType == "" {
		eventType = "message"
	}
	messageID := eventString(payload, "message_id", "messageId")
	memberKey := eventString(payload, "member_id", "memberId", "member_key")
	if isOutboundTeamEvent(eventType) && messageID != "" && !teamEventHasBody(payload) && bus != nil {
		enriched, err := s.enrichOutboundEventFromInbox(team.ID, bus, payload, messageID)
		if err != nil {
			fmt.Printf("Warning: failed to enrich Team %d outbound event %s from inbox: %v\n", team.ID, messageID, err)
		} else {
			payload = enriched
		}
	}

	var member *models.TeamMember
	if memberKey != "" {
		found, err := s.repo.GetMemberByTeamKey(team.ID, memberKey)
		if err != nil {
			return err
		}
		member = found
	}

	var task *models.TeamTask
	if taskID := eventInt(payload, "task_id", "taskId"); taskID > 0 {
		found, err := s.repo.GetTaskByID(taskID)
		if err != nil {
			return err
		}
		if found != nil && found.TeamID == team.ID {
			task = found
		}
	}
	if task == nil && messageID != "" {
		found, err := s.repo.GetTaskByMessageID(team.ID, messageID)
		if err != nil {
			return err
		}
		task = found
	}
	eventType = normalizeFinalReplyTaskEvent(eventType, payload, task)

	payloadJSON, err := marshalOptionalJSON(payload)
	if err != nil {
		return err
	}
	streamID := message.ID
	event := &models.TeamEvent{
		TeamID:        team.ID,
		EventType:     eventType,
		PayloadJSON:   payloadJSON,
		RedisStreamID: &streamID,
		OccurredAt:    eventTime(payload),
	}
	if member != nil {
		event.MemberID = &member.ID
	}
	if task != nil {
		event.TaskID = &task.ID
	}
	if messageID != "" {
		event.MessageID = &messageID
	}
	if err := s.repo.CreateEvent(event); err != nil {
		return err
	}

	now := time.Now().UTC()
	if task != nil {
		switch eventType {
		case "task_received":
			if task.Status == models.TeamTaskStatusPending {
				task.Status = models.TeamTaskStatusDispatched
			}
		case "task_started":
			if task.Status != models.TeamTaskStatusStale {
				task.Status = models.TeamTaskStatusRunning
				task.StartedAt = &now
			}
		case "task_completed":
			task.Status = models.TeamTaskStatusSucceeded
			task.FinishedAt = &now
			task.ResultJSON = payloadJSON
		case "task_failed", "message_failed":
			task.Status = models.TeamTaskStatusFailed
			task.FinishedAt = &now
			if errText := eventString(payload, "error_message", "error"); errText != "" {
				task.ErrorMessage = &errText
			}
		}
		task.UpdatedAt = now
		if err := s.repo.UpdateTask(task); err != nil {
			return err
		}
	}
	if member != nil {
		member.LastSeenAt = &now
		applyTeamMemberRuntimeProjection(member, payload, eventType)
		if task != nil && (eventType == "task_received" || eventType == "task_started") {
			member.Status = models.TeamMemberStatusBusy
			if member.Availability == "" || member.Availability == models.TeamMemberAvailabilityUnknown {
				member.Availability = models.TeamMemberAvailabilityBusy
			}
			member.CurrentTaskID = &task.ID
			member.Progress = eventInt(payload, "progress")
		}
		if eventType == "task_completed" || eventType == "task_failed" || eventType == "message_failed" {
			member.Status = models.TeamMemberStatusIdle
			member.CurrentTaskID = nil
			if eventType == "task_completed" {
				member.Progress = 100
				if member.Availability != models.TeamMemberAvailabilityBlocked {
					member.Availability = models.TeamMemberAvailabilityIdle
					member.BlockedReason = nil
				}
			} else {
				member.Progress = 0
				if member.Availability == "" || member.Availability == models.TeamMemberAvailabilityUnknown {
					member.Availability = models.TeamMemberAvailabilityBlocked
				}
				if member.BlockedReason == nil {
					if errText := eventString(payload, "error_message", "error", "reason", "diagnostic", "lastSummary", "last_summary"); errText != "" {
						member.BlockedReason = &errText
					}
				}
			}
		}
		member.UpdatedAt = now
		if err := s.repo.UpdateMember(member); err != nil {
			return err
		}
	}
	return nil
}

func normalizeFinalReplyTaskEvent(eventType string, payload map[string]interface{}, task *models.TeamTask) string {
	if task == nil || !strings.EqualFold(strings.TrimSpace(eventType), "reply") {
		return eventType
	}
	if !eventBool(payload, "final", "isFinal", "complete", "completed", "taskCompleted") {
		return eventType
	}
	if !teamEventHasBody(payload) {
		return eventType
	}
	payload["originalEvent"] = eventType
	payload["event"] = "task_completed"
	payload["type"] = "task_completed"
	payload["status"] = "succeeded"
	payload["availability"] = models.TeamMemberAvailabilityIdle
	payload["runtimeStatus"] = "succeeded"
	if eventString(payload, "resultMarkdown") == "" {
		if text := eventString(payload, "text", "result", "summary"); text != "" {
			payload["resultMarkdown"] = text
		}
	}
	if eventString(payload, "summary") == "" {
		if text := eventString(payload, "text", "resultMarkdown", "result"); text != "" {
			payload["summary"] = text
		}
	}
	return "task_completed"
}

func (s *teamService) enrichOutboundEventFromInbox(teamID int, bus *redisBus, payload map[string]interface{}, messageID string) (map[string]interface{}, error) {
	targetMember := eventString(payload, "to", "recipient", "target", "targetMemberId", "target_member_id")
	if targetMember == "" {
		return payload, nil
	}
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 {
			time.Sleep(100 * time.Millisecond)
		}
		messages, err := bus.XRevRange(context.Background(), teamInboxKey(teamID, targetMember), 100)
		if err != nil {
			lastErr = err
			continue
		}
		for _, inboxMessage := range messages {
			if !redisStreamMessageMatches(inboxMessage, messageID) {
				continue
			}
			envelope := mergeRedisEventPayload(inboxMessage.Fields)
			return mergeMissingEventFields(payload, envelope), nil
		}
	}
	return payload, lastErr
}

func redisStreamMessageMatches(message redisStreamMessage, messageID string) bool {
	if strings.TrimSpace(message.Fields["message_id"]) == messageID {
		return true
	}
	payload := mergeRedisEventPayload(message.Fields)
	return eventString(payload, "message_id", "messageId") == messageID
}

func mergeMissingEventFields(base map[string]interface{}, extra map[string]interface{}) map[string]interface{} {
	merged := map[string]interface{}{}
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range extra {
		if existing, ok := merged[key]; !ok || isEmptyEventValue(existing) {
			merged[key] = value
		}
	}
	if metadata, ok := extra["metadata"].(map[string]interface{}); ok {
		for key, value := range metadata {
			if existing, ok := merged[key]; !ok || isEmptyEventValue(existing) {
				merged[key] = value
			}
		}
	}
	return merged
}

func isEmptyEventValue(value interface{}) bool {
	if value == nil {
		return true
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text) == ""
	}
	return false
}

func isOutboundTeamEvent(eventType string) bool {
	switch eventType {
	case "outbound", "task_assigned":
		return true
	default:
		return false
	}
}

func teamEventHasBody(payload map[string]interface{}) bool {
	if eventString(payload, "text", "title", "prompt", "instruction", "instructions", "summary", "resultMarkdown") != "" {
		return true
	}
	for _, key := range []string{"sent", "metadata", "data", "envelope", "task"} {
		record, ok := payload[key].(map[string]interface{})
		if !ok {
			continue
		}
		if eventString(record, "text", "title", "prompt", "instruction", "instructions", "summary", "resultMarkdown") != "" {
			return true
		}
	}
	return false
}

func (s *teamService) markTeamFailed(team *models.Team, cause error) error {
	team.Status = models.TeamStatusFailed
	team.UpdatedAt = time.Now().UTC()
	_ = s.repo.UpdateTeam(team)
	return cause
}

func (s *teamService) rollbackTeamCreation(userID int, team *models.Team, cause error) error {
	members, err := s.repo.ListMembersByTeamID(team.ID)
	if err != nil {
		fmt.Printf("Warning: failed to list Team %d members during create rollback: %v\n", team.ID, err)
	}
	for idx := range members {
		member := members[idx]
		if member.InstanceID != nil && *member.InstanceID > 0 {
			if err := s.instanceService.Delete(*member.InstanceID); err != nil {
				fmt.Printf("Warning: failed to delete Team %d member %s instance %d during create rollback: %v\n", team.ID, member.MemberKey, *member.InstanceID, err)
			}
		}
		member.Status = models.TeamMemberStatusDeleted
		member.CurrentTaskID = nil
		member.UpdatedAt = time.Now().UTC()
		_ = s.repo.UpdateMember(&member)
	}

	ctx := context.Background()
	if strings.TrimSpace(derefTeamString(team.TeamTokenSecretName)) != "" {
		if err := s.secretService.DeleteSecret(ctx, userID, derefTeamString(team.TeamTokenSecretName)); err != nil {
			fmt.Printf("Warning: failed to delete Team %d secret during create rollback: %v\n", team.ID, err)
		}
	}
	if err := s.configMapService.DeleteConfigMap(ctx, userID, s.teamConfigMapName(team.ID)); err != nil {
		fmt.Printf("Warning: failed to delete Team %d configmap during create rollback: %v\n", team.ID, err)
	}
	if err := s.pvcService.DeleteTeamSharedPVC(ctx, userID, team.ID); err != nil {
		fmt.Printf("Warning: failed to delete Team %d shared PVC during create rollback: %v\n", team.ID, err)
	}

	team.Name = deletedTeamName(team.Name, team.ID)
	team.Status = models.TeamStatusDeleted
	team.UpdatedAt = time.Now().UTC()
	if err := s.repo.UpdateTeam(team); err != nil {
		fmt.Printf("Warning: failed to mark Team %d deleted during create rollback: %v\n", team.ID, err)
	}
	return cause
}

func teamTaskPayloads(tasks []models.TeamTask) []TeamTaskPayload {
	result := make([]TeamTaskPayload, 0, len(tasks))
	for _, task := range tasks {
		if payload, err := teamTaskPayload(task); err == nil {
			result = append(result, *payload)
		}
	}
	return result
}

func normalizeTeamHistoryLimit(limit, defaultLimit, maxLimit int) int {
	if limit <= 0 {
		return defaultLimit
	}
	if limit > maxLimit {
		return maxLimit
	}
	return limit
}

func nextTeamTaskBeforeID(tasks []TeamTaskPayload) *int {
	if len(tasks) == 0 {
		return nil
	}
	next := tasks[len(tasks)-1].ID
	return &next
}

func nextTeamEventBeforeID(events []TeamEventPayload) *int {
	if len(events) == 0 {
		return nil
	}
	next := events[len(events)-1].ID
	return &next
}

func teamTaskPayload(task models.TeamTask) (*TeamTaskPayload, error) {
	payload := &TeamTaskPayload{TeamTask: task}
	if strings.TrimSpace(task.PayloadJSON) != "" {
		if err := json.Unmarshal([]byte(task.PayloadJSON), &payload.Payload); err != nil {
			return nil, err
		}
	}
	if task.ResultJSON != nil && strings.TrimSpace(*task.ResultJSON) != "" {
		if err := json.Unmarshal([]byte(*task.ResultJSON), &payload.Result); err != nil {
			return nil, err
		}
	}
	return payload, nil
}

func buildInitialLeaderTaskPayload(teamName string) map[string]interface{} {
	normalizedTeamName := strings.TrimSpace(teamName)
	if normalizedTeamName == "" {
		normalizedTeamName = "current"
	}
	prompt := fmt.Sprintf("请介绍`team %s`当前 Redis Team成员构成，包括各角色的职责分工、运行状态与技术能力边界。同时说明团队内部的协作与通信机制(team_send)，例如任务流转方式、消息同步方式、上下文共享方式以及可调用的方法、工具与操作能力，以便后续能够更高效地开展团队工作", normalizedTeamName)
	return map[string]interface{}{
		"intent": initialLeaderTaskIntent,
		"title":  "介绍当前 Redis Team 成员与协作机制",
		"prompt": prompt,
	}
}

func teamEventPayloads(events []models.TeamEvent) []TeamEventPayload {
	result := make([]TeamEventPayload, 0, len(events))
	for _, event := range events {
		payload := TeamEventPayload{TeamEvent: event}
		if event.PayloadJSON != nil && strings.TrimSpace(*event.PayloadJSON) != "" {
			_ = json.Unmarshal([]byte(*event.PayloadJSON), &payload.Payload)
		}
		result = append(result, payload)
	}
	return result
}

func mergeRedisEventPayload(fields map[string]string) map[string]interface{} {
	payload := map[string]interface{}{}
	if raw := strings.TrimSpace(fields["payload"]); raw != "" {
		_ = json.Unmarshal([]byte(raw), &payload)
	}
	for key, value := range fields {
		if _, exists := payload[key]; !exists {
			payload[key] = value
		}
	}
	return payload
}

func eventString(payload map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case string:
			return strings.TrimSpace(typed)
		case float64:
			return strconv.Itoa(int(typed))
		case int:
			return strconv.Itoa(typed)
		default:
			return strings.TrimSpace(fmt.Sprintf("%v", typed))
		}
	}
	return ""
}

func eventBool(payload map[string]interface{}, keys ...string) bool {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case bool:
			return typed
		case string:
			switch strings.ToLower(strings.TrimSpace(typed)) {
			case "1", "true", "yes", "y", "on":
				return true
			case "0", "false", "no", "n", "off":
				return false
			}
		case float64:
			return typed != 0
		case int:
			return typed != 0
		default:
			text := strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", typed)))
			if text == "true" || text == "yes" || text == "1" {
				return true
			}
		}
	}
	return false
}

func applyTeamMemberRuntimeProjection(member *models.TeamMember, payload map[string]interface{}, eventType string) {
	if member == nil {
		return
	}
	availability := normalizeTeamAvailability(eventString(payload, "availability", "memberAvailability"))
	explicitlyBlocked := availability == models.TeamMemberAvailabilityBlocked
	if availability != "" {
		member.Availability = availability
	}
	if member.Availability == "" {
		member.Availability = models.TeamMemberAvailabilityUnknown
	}
	if runtimeStatus := eventString(payload, "runtime_status", "runtimeStatus", "runtime", "liveness"); runtimeStatus != "" {
		member.RuntimeStatus = &runtimeStatus
	}
	if runtimeTaskID := eventString(payload, "runtime_task_id", "runtimeTaskId", "current_task_id", "currentTaskId", "taskId"); runtimeTaskID != "" {
		member.RuntimeTaskID = &runtimeTaskID
	}
	if runtimeIntent := eventString(payload, "runtime_intent", "runtimeIntent", "current_intent", "currentIntent", "intent"); runtimeIntent != "" {
		member.RuntimeIntent = &runtimeIntent
	}
	if summary := eventString(payload, "last_summary", "lastSummary", "summary", "diagnostic"); summary != "" {
		member.LastSummary = &summary
	}
	if reason := eventString(payload, "blocked_reason", "blockedReason", "error_message", "error", "reason"); reason != "" {
		member.BlockedReason = &reason
	}
	switch eventType {
	case "presence", "member_presence", "status", "member_status":
		return
	case "task_completed":
		if !explicitlyBlocked {
			member.Availability = models.TeamMemberAvailabilityIdle
			member.BlockedReason = nil
		}
	case "task_failed", "message_failed":
		if member.Availability == "" || member.Availability == models.TeamMemberAvailabilityUnknown || member.Availability == models.TeamMemberAvailabilityBusy {
			member.Availability = models.TeamMemberAvailabilityBlocked
		}
	}
}

func normalizeTeamAvailability(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "idle", "available", "ready":
		return models.TeamMemberAvailabilityIdle
	case "busy", "running", "working":
		return models.TeamMemberAvailabilityBusy
	case "blocked", "error", "failed":
		return models.TeamMemberAvailabilityBlocked
	case "offline", "unavailable":
		return models.TeamMemberAvailabilityOffline
	case "unknown":
		return models.TeamMemberAvailabilityUnknown
	default:
		return ""
	}
}

func eventInt(payload map[string]interface{}, keys ...string) int {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case float64:
			return int(typed)
		case int:
			return typed
		case string:
			parsed, _ := strconv.Atoi(strings.TrimSpace(typed))
			return parsed
		}
	}
	return 0
}

func eventTime(payload map[string]interface{}) *time.Time {
	for _, key := range []string{"occurred_at", "occurredAt", "timestamp"} {
		raw := eventString(payload, key)
		if raw == "" {
			continue
		}
		if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			return &parsed
		}
	}
	now := time.Now().UTC()
	return &now
}

func normalizeContextRefs(value interface{}) []string {
	rawItems, ok := value.([]interface{})
	if !ok {
		if typed, ok := value.([]string); ok {
			return typed
		}
		return nil
	}
	refs := make([]string, 0, len(rawItems))
	for _, item := range rawItems {
		ref := strings.TrimSpace(fmt.Sprintf("%v", item))
		if ref != "" {
			refs = append(refs, ref)
		}
	}
	return refs
}

func planTeamMembers(teamName string, members []CreateTeamMemberRequest) ([]plannedTeamMember, error) {
	plans := make([]plannedTeamMember, 0, len(members))
	memberKeys := map[string]struct{}{}
	leaderCount := 0
	for idx, memberReq := range members {
		role := normalizeTeamMemberRole(memberReq.Role, memberReq.IsLeader)
		memberKey, err := normalizeTeamMemberKey(memberReq.MemberID, role, idx)
		if err != nil {
			return nil, err
		}
		if _, exists := memberKeys[memberKey]; exists {
			return nil, fmt.Errorf("duplicate team member id: %s", memberKey)
		}
		memberKeys[memberKey] = struct{}{}
		runtimeType, err := normalizeTeamMemberRuntimeType(memberReq.RuntimeType)
		if err != nil {
			return nil, err
		}
		instanceMode, err := normalizeTeamMemberInstanceMode(memberReq.Mode, memberReq.InstanceMode)
		if err != nil {
			return nil, err
		}

		isLeader := memberReq.IsLeader || isTeamLeaderRole(role)
		if isLeader {
			leaderCount++
			role = "leader"
		}
		displayName := strings.TrimSpace(memberReq.Name)
		if displayName == "" {
			displayName = fmt.Sprintf("%s-%s", teamName, memberKey)
		}
		plans = append(plans, plannedTeamMember{
			Request:      memberReq,
			MemberKey:    memberKey,
			DisplayName:  displayName,
			Role:         role,
			RuntimeType:  runtimeType,
			InstanceMode: instanceMode,
			IsLeader:     isLeader,
		})
	}
	if leaderCount != 1 {
		return nil, fmt.Errorf("team must include exactly one leader")
	}
	return plans, nil
}

func teamMemberInstanceName(teamName string, teamID int, memberKey string) string {
	teamPart := normalizeTeamMemberKeyForInstanceName(teamName)
	if teamPart == "" {
		teamPart = "team"
	}
	memberPart := normalizeTeamMemberKeyForInstanceName(memberKey)
	if memberPart == "" {
		memberPart = "member"
	}
	const maxInstanceNameLength = 50
	idPart := fmt.Sprintf("%d", teamID)
	maxMemberLength := maxInstanceNameLength - len(idPart) - len("--t")
	if maxMemberLength < 1 {
		maxMemberLength = 1
	}
	if len(memberPart) > maxMemberLength {
		memberPart = strings.Trim(memberPart[:maxMemberLength], "-")
		if memberPart == "" {
			memberPart = "member"
		}
	}
	suffix := fmt.Sprintf("-%s-%s", idPart, memberPart)
	if len(teamPart)+len(suffix) <= maxInstanceNameLength {
		return teamPart + suffix
	}
	maxTeamLength := maxInstanceNameLength - len(suffix)
	if maxTeamLength < 1 {
		maxTeamLength = 1
	}
	return strings.Trim(teamPart[:maxTeamLength], "-") + suffix
}

func normalizeTeamMemberKeyForInstanceName(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "_", "-")
	normalized = strings.ReplaceAll(normalized, " ", "-")
	normalized = teamMemberInstanceNameInvalidChars.ReplaceAllString(normalized, "")
	normalized = teamMemberInstanceNameRepeatedDashs.ReplaceAllString(normalized, "-")
	return strings.Trim(normalized, "-")
}

func normalizeTeamMemberRuntimeType(raw string) (string, error) {
	runtimeType := strings.ToLower(strings.TrimSpace(raw))
	if runtimeType == "" {
		return "openclaw", nil
	}
	switch runtimeType {
	case "openclaw", "hermes":
		return runtimeType, nil
	default:
		return "", fmt.Errorf("unsupported team member runtime type: %s", raw)
	}
}

func normalizeTeamMemberInstanceMode(rawMode, rawInstanceMode string) (string, error) {
	if mode, ok := NormalizeInstanceMode(rawMode); ok {
		return mode, nil
	}
	if strings.TrimSpace(rawMode) != "" {
		return "", fmt.Errorf("unsupported team member instance mode: %s", rawMode)
	}
	if mode, ok := NormalizeInstanceMode(rawInstanceMode); ok {
		return mode, nil
	}
	if strings.TrimSpace(rawInstanceMode) != "" {
		return "", fmt.Errorf("unsupported team member instance mode: %s", rawInstanceMode)
	}
	return InstanceModeLite, nil
}

func normalizeTeamMemberRole(raw string, isLeader bool) string {
	role := strings.TrimSpace(raw)
	if isLeader || isTeamLeaderRole(role) {
		return "leader"
	}
	if role == "" {
		return "member"
	}
	return role
}

func isTeamLeaderRole(role string) bool {
	normalized := strings.ToLower(strings.TrimSpace(role))
	normalized = strings.ReplaceAll(normalized, "_", "-")
	normalized = strings.ReplaceAll(normalized, " ", "-")
	return normalized == "leader" || normalized == "team-leader"
}

func findTeamLeader(members []models.TeamMember) *models.TeamMember {
	for idx := range members {
		if isTeamLeaderRole(members[idx].Role) {
			member := members[idx]
			return &member
		}
	}
	return nil
}

func leaderMemberKey(member *models.TeamMember) string {
	if member == nil {
		return ""
	}
	return member.MemberKey
}

type teamRosterConfig struct {
	Version           int                `json:"version"`
	TeamID            string             `json:"teamId"`
	LeaderMemberID    string             `json:"leaderMemberId"`
	CommunicationMode string             `json:"communicationMode"`
	SharedDir         string             `json:"sharedDir"`
	Members           []teamRosterMember `json:"members"`
	Redis             teamRosterRedis    `json:"redis"`
}

type teamRosterMember struct {
	MemberID     string `json:"memberId"`
	Role         string `json:"role"`
	RuntimeType  string `json:"runtimeType"`
	InstanceMode string `json:"instanceMode"`
	DisplayName  string `json:"displayName"`
	Description  string `json:"description,omitempty"`
	IsLeader     bool   `json:"isLeader"`
}

type teamRosterRedis struct {
	EventsKey   string `json:"eventsKey"`
	PresenceKey string `json:"presenceKey"`
	DLQKey      string `json:"dlqKey"`
}

func buildTeamRosterConfig(team *models.Team, members []plannedTeamMember) (string, error) {
	config := teamRosterConfig{
		Version:           1,
		TeamID:            strconv.Itoa(team.ID),
		CommunicationMode: team.CommunicationMode,
		SharedDir:         team.SharedMountPath,
		Members:           make([]teamRosterMember, 0, len(members)),
		Redis: teamRosterRedis{
			EventsKey:   teamEventsKey(team.ID),
			PresenceKey: teamPresenceKey(team.ID),
			DLQKey:      teamDLQKey(team.ID),
		},
	}
	for _, member := range members {
		if member.IsLeader {
			config.LeaderMemberID = member.MemberKey
		}
		config.Members = append(config.Members, teamRosterMember{
			MemberID:     member.MemberKey,
			Role:         member.Role,
			RuntimeType:  member.RuntimeType,
			InstanceMode: member.InstanceMode,
			DisplayName:  member.DisplayName,
			Description:  derefTeamString(member.Request.Description),
			IsLeader:     member.IsLeader,
		})
	}
	if config.LeaderMemberID == "" {
		return "", fmt.Errorf("team must include exactly one leader")
	}
	return marshalJSON(config)
}

func rosterJSONWithSharedDir(rosterJSON, sharedDir string) string {
	sharedDir = strings.TrimSpace(sharedDir)
	if sharedDir == "" {
		return rosterJSON
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(rosterJSON), &payload); err != nil {
		return rosterJSON
	}
	payload["sharedDir"] = sharedDir
	raw, err := json.Marshal(payload)
	if err != nil {
		return rosterJSON
	}
	return string(raw)
}

func buildTeamRosterConfigFromMembers(team *models.Team, members []models.TeamMember) (string, error) {
	config := teamRosterConfig{
		Version:           1,
		TeamID:            strconv.Itoa(team.ID),
		CommunicationMode: team.CommunicationMode,
		SharedDir:         team.SharedMountPath,
		Members:           make([]teamRosterMember, 0, len(members)),
		Redis: teamRosterRedis{
			EventsKey:   teamEventsKey(team.ID),
			PresenceKey: teamPresenceKey(team.ID),
			DLQKey:      teamDLQKey(team.ID),
		},
	}
	for _, member := range members {
		isLeader := isTeamLeaderRole(member.Role)
		runtimeType := strings.TrimSpace(member.RuntimeType)
		if runtimeType == "" {
			runtimeType = "openclaw"
		}
		instanceMode := strings.TrimSpace(member.InstanceMode)
		if instanceMode == "" {
			instanceMode = InstanceModeLite
		}
		if isLeader {
			config.LeaderMemberID = member.MemberKey
		}
		config.Members = append(config.Members, teamRosterMember{
			MemberID:     member.MemberKey,
			Role:         member.Role,
			RuntimeType:  runtimeType,
			InstanceMode: instanceMode,
			DisplayName:  member.DisplayName,
			Description:  derefTeamString(member.Description),
			IsLeader:     isLeader,
		})
	}
	if config.LeaderMemberID == "" {
		return "", fmt.Errorf("team must include exactly one leader")
	}
	return marshalJSON(config)
}

func activeTeamMembers(members []models.TeamMember) []models.TeamMember {
	active := make([]models.TeamMember, 0, len(members))
	for _, member := range members {
		if member.Status == models.TeamMemberStatusDeleted || member.Status == models.TeamMemberStatusDeleting {
			continue
		}
		active = append(active, member)
	}
	return active
}

func activeTeams(teams []models.Team) []models.Team {
	active := make([]models.Team, 0, len(teams))
	for _, team := range teams {
		if team.Status == models.TeamStatusDeleted {
			continue
		}
		active = append(active, team)
	}
	return active
}

func deletedTeamName(name string, teamID int) string {
	const maxTeamNameLength = 255
	suffix := fmt.Sprintf("__deleted_%d", teamID)
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		trimmed = "team"
	}
	if strings.HasSuffix(trimmed, suffix) {
		return trimmed
	}
	if len(trimmed)+len(suffix) <= maxTeamNameLength {
		return trimmed + suffix
	}
	runes := []rune(trimmed)
	maxPrefixLength := maxTeamNameLength - len(suffix)
	if len(runes) > maxPrefixLength {
		runes = runes[:maxPrefixLength]
	}
	return string(runes) + suffix
}

func normalizeTeamMemberKey(raw, role string, index int) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		value = strings.ToLower(strings.TrimSpace(role))
	}
	if value == "" {
		value = fmt.Sprintf("member-%d", index+1)
	}
	value = strings.ReplaceAll(value, "_", "-")
	value = strings.ReplaceAll(value, " ", "-")
	if !teamMemberKeyPattern.MatchString(value) {
		return "", fmt.Errorf("team member id is invalid")
	}
	return value, nil
}

func defaultTeamRedisURL() string {
	for _, key := range []string{"CLAWMANAGER_TEAM_REDIS_URL", "TEAM_REDIS_URL", "REDIS_URL"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	if value, ok := defaultTeamRedisServiceURL(); ok {
		return value
	}
	return ""
}

func defaultTeamRedisServiceURL() (string, bool) {
	systemNamespace := strings.TrimSpace(os.Getenv("CLAWMANAGER_SYSTEM_NAMESPACE"))
	if systemNamespace == "" {
		if client := k8s.GetClient(); client != nil {
			systemNamespace = client.GetSystemNamespace()
		} else if baseNamespace := strings.TrimSpace(os.Getenv("K8S_NAMESPACE")); baseNamespace != "" {
			systemNamespace = fmt.Sprintf("%s-system", baseNamespace)
		}
	}
	if systemNamespace == "" {
		return "", false
	}

	serviceName := strings.TrimSpace(os.Getenv("CLAWMANAGER_TEAM_REDIS_SERVICE_NAME"))
	if serviceName == "" {
		serviceName = strings.TrimSpace(os.Getenv("CLAWMANAGER_TEAM_REDIS_SERVICE"))
	}
	if serviceName == "" {
		serviceName = "clawmanager-team-redis"
	}

	port := normalizePortValue(
		strings.TrimSpace(os.Getenv("CLAWMANAGER_TEAM_REDIS_SERVICE_PORT")),
		strings.TrimSpace(os.Getenv("CLAWMANAGER_TEAM_REDIS_PORT")),
	)
	if port == "" {
		port = "6379"
	}

	db := strings.TrimSpace(os.Getenv("CLAWMANAGER_TEAM_REDIS_DB"))
	if db == "" {
		db = strings.TrimSpace(os.Getenv("TEAM_REDIS_DB"))
	}
	if db == "" {
		db = "0"
	}

	return fmt.Sprintf("redis://%s.%s.svc.cluster.local:%s/%s", serviceName, systemNamespace, port, db), true
}

func teamTaskStaleTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv("CLAWMANAGER_TEAM_TASK_STALE_SECONDS"))
	if raw == "" {
		return defaultTeamTaskStaleTimeout
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil {
		return defaultTeamTaskStaleTimeout
	}
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

func defaultTeamManagerBaseURL() (string, bool) {
	if override := strings.TrimSpace(os.Getenv("CLAWMANAGER_TEAM_MANAGER_BASE_URL")); override != "" {
		return override, true
	}
	return defaultAgentControlBaseURL()
}

func teamInboxKey(teamID int, memberID string) string {
	return fmt.Sprintf("claw:team:%d:inbox:%s", teamID, memberID)
}

func teamEventsKey(teamID int) string {
	return fmt.Sprintf("claw:team:%d:events", teamID)
}

func teamPresenceKey(teamID int) string {
	return fmt.Sprintf("claw:team:%d:presence", teamID)
}

func teamDLQKey(teamID int) string {
	return fmt.Sprintf("claw:team:%d:dlq", teamID)
}

func defaultInt(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func defaultFloat(value, fallback float64) float64 {
	if value > 0 {
		return value
	}
	return fallback
}

func derefTeamString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}
