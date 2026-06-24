package services

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"clawreef/internal/models"
)

func TestTeamMemberEnvUsesSecretBackedRedisAndToken(t *testing.T) {
	t.Setenv("CLAWMANAGER_TEAM_MANAGER_BASE_URL", "http://manager.example")

	service := &teamService{}
	env := service.teamMemberEnv(&models.Team{
		ID:              12,
		SharedMountPath: "/team",
	}, "leader", "lead")

	if env["CLAWMANAGER_TEAM_ID"] != "12" {
		t.Fatalf("expected Team id env, got %q", env["CLAWMANAGER_TEAM_ID"])
	}
	if env["CLAWMANAGER_TEAM_MEMBER_ID"] != "leader" {
		t.Fatalf("expected member id env, got %q", env["CLAWMANAGER_TEAM_MEMBER_ID"])
	}
	if env["CLAWMANAGER_TEAM_ROLE"] != "lead" {
		t.Fatalf("expected Team role env, got %q", env["CLAWMANAGER_TEAM_ROLE"])
	}
	if env["CLAWMANAGER_TEAM_INBOX_KEY"] != "claw:team:12:inbox:leader" {
		t.Fatalf("unexpected inbox key: %q", env["CLAWMANAGER_TEAM_INBOX_KEY"])
	}
	if env["CLAWMANAGER_TEAM_EVENTS_KEY"] != "claw:team:12:events" {
		t.Fatalf("unexpected events key: %q", env["CLAWMANAGER_TEAM_EVENTS_KEY"])
	}
	if env["CLAWMANAGER_TEAM_MANAGER_URL"] != "http://manager.example" {
		t.Fatalf("unexpected manager url: %q", env["CLAWMANAGER_TEAM_MANAGER_URL"])
	}
	if env["CLAWMANAGER_TEAM_CONFIG_PATH"] != "/etc/clawmanager/team/team.json" {
		t.Fatalf("unexpected Team config path: %q", env["CLAWMANAGER_TEAM_CONFIG_PATH"])
	}
	if env["CLAWMANAGER_TEAM_SHARED_UID"] != "1000" || env["CLAWMANAGER_TEAM_SHARED_GID"] != "1000" || env["CLAWMANAGER_TEAM_UMASK"] != "0002" {
		t.Fatalf("expected Team shared permission env, got %#v", env)
	}
	if env["PUID"] != "1000" || env["PGID"] != "1000" || env["UMASK"] != "0002" {
		t.Fatalf("expected runtime shared permission env, got %#v", env)
	}
	if env["CLAWMANAGER_TEAM_AUTORUN"] != "true" || env["CLAWMANAGER_TEAM_CONSUMER_GROUP"] != "team-members" {
		t.Fatalf("expected Team autorun and consumer group env, got %#v", env)
	}
	for key := range env {
		if strings.Contains(key, "REDIS_URL") || strings.Contains(key, "TOKEN") {
			t.Fatalf("sensitive Team env %s must come from Secret, not plain env", key)
		}
	}
}

func TestBuildTeamMemberInstanceRequestUsesSharedPermissionDefaults(t *testing.T) {
	service := &teamService{}
	pvcName := "clawreef-team-7-shared"
	secretName := "clawreef-team-7-bus"
	req := service.buildTeamMemberInstanceRequest(&models.Team{
		ID:                  7,
		Name:                "Delivery",
		SharedPVCName:       &pvcName,
		SharedMountPath:     "/team",
		TeamTokenSecretName: &secretName,
	}, plannedTeamMember{
		MemberKey:   "delivery-lead",
		DisplayName: "delivery lead",
		Role:        "leader",
		RuntimeType: "openclaw",
	})

	if req.Team == nil {
		t.Fatalf("expected Team instance config")
	}
	if req.Team.SharedUID != 1000 || req.Team.SharedGID != 1000 || req.Team.SharedUmask != "0002" {
		t.Fatalf("unexpected Team shared permission config: %#v", req.Team)
	}
	if req.Team.ConfigMountPath != "/etc/clawmanager/team" {
		t.Fatalf("unexpected Team config mount path: %q", req.Team.ConfigMountPath)
	}
	if req.Team.Environment["CLAWMANAGER_TEAM_CONFIG_PATH"] != "/etc/clawmanager/team/team.json" {
		t.Fatalf("unexpected Team config env: %#v", req.Team.Environment)
	}
}

func TestBuildTeamMemberInstanceRequestSupportsLiteMode(t *testing.T) {
	service := &teamService{runtimeWorkspaceRoot: "/workspaces"}
	team := &models.Team{
		UserID:          1,
		ID:              8,
		Name:            "Lite Team",
		SharedMountPath: "/team",
	}
	memberPlan := plannedTeamMember{
		Request: CreateTeamMemberRequest{
			Mode:         "Lite",
			InstanceMode: "Lite",
		},
		MemberKey:    "lite-worker",
		DisplayName:  "lite worker",
		Role:         "developer",
		RuntimeType:  "openclaw",
		InstanceMode: InstanceModeLite,
	}
	req := service.buildTeamMemberInstanceRequest(team, memberPlan)

	if req.Mode != InstanceModeLite || req.InstanceMode != InstanceModeLite {
		t.Fatalf("expected Team member instance request to preserve lite mode, got mode=%q instance_mode=%q", req.Mode, req.InstanceMode)
	}
	if req.RuntimeType != RuntimeBackendGateway {
		t.Fatalf("expected lite Team member to target gateway runtime, got %q", req.RuntimeType)
	}

	rosterJSON := `{"teamId":"8","members":[{"memberId":"lite-worker"}]}`
	liteReq := service.buildTeamMemberInstanceRequestWithSecrets(team, memberPlan, &teamRuntimeSecrets{
		RedisURL: "redis://team-redis:6379/0",
		Token:    "team_test_token",
	}, rosterJSON)
	if liteReq.EnvironmentOverrides[teamRedisURLSecretKey] != "redis://team-redis:6379/0" ||
		liteReq.EnvironmentOverrides[teamTokenSecretKey] != "team_test_token" {
		t.Fatalf("expected Lite Team runtime secrets in gateway env overrides, got %#v", liteReq.EnvironmentOverrides)
	}
	if !strings.Contains(liteReq.EnvironmentOverrides["CLAWMANAGER_TEAM_CONFIG_JSON"], `"sharedDir":"/workspaces/teams/user-1/team-8-shared"`) {
		t.Fatalf("expected Lite Team roster JSON to include runtime shared dir, got %#v", liteReq.EnvironmentOverrides)
	}
}

func TestBuildTeamMemberInstanceRequestPointsLiteSharedDirAtRuntimeWorkspace(t *testing.T) {
	service := &teamService{runtimeWorkspaceRoot: "/workspaces"}
	team := &models.Team{
		UserID:          1,
		ID:              28,
		Name:            "Mixed Team",
		SharedMountPath: "/team",
	}
	memberPlan := plannedTeamMember{
		MemberKey:    "backend",
		DisplayName:  "backend",
		Role:         "developer",
		RuntimeType:  "openclaw",
		InstanceMode: InstanceModeLite,
	}

	req := service.buildTeamMemberInstanceRequestWithSecrets(team, memberPlan, &teamRuntimeSecrets{
		RedisURL: "redis://team-redis:6379/0",
		Token:    "team_test_token",
	}, `{"sharedDir":"/team"}`)

	wantSharedDir := "/workspaces/teams/user-1/team-28-shared"
	if req.EnvironmentOverrides["CLAWMANAGER_TEAM_SHARED_DIR"] != wantSharedDir {
		t.Fatalf("expected Lite shared dir %q, got %#v", wantSharedDir, req.EnvironmentOverrides)
	}
	if !strings.Contains(req.EnvironmentOverrides["CLAWMANAGER_TEAM_CONFIG_JSON"], `"sharedDir":"`+wantSharedDir+`"`) {
		t.Fatalf("expected Lite roster JSON to use runtime shared dir, got %s", req.EnvironmentOverrides["CLAWMANAGER_TEAM_CONFIG_JSON"])
	}
	if req.Team.SharedMountPath != "/team" {
		t.Fatalf("Pro Team mount path should remain /team, got %q", req.Team.SharedMountPath)
	}
}

func TestOpenClawConfigPlanForTeamMemberFiltersOnlyWorkers(t *testing.T) {
	originalPlan := &OpenClawConfigPlan{
		Mode:        OpenClawConfigPlanModeManual,
		ResourceIDs: []int{10, 20},
	}
	filteredPlan := &OpenClawConfigPlan{
		Mode:        OpenClawConfigPlanModeManual,
		ResourceIDs: []int{20},
	}
	planner := &teamOpenClawConfigPlannerStub{nextPlan: filteredPlan}
	service := &teamService{openClawConfigPlanner: planner}

	leaderPlan, err := service.openClawConfigPlanForTeamMember(7, plannedTeamMember{
		IsLeader: true,
		Request:  CreateTeamMemberRequest{OpenClawConfigPlan: originalPlan},
	})
	if err != nil {
		t.Fatalf("leader plan returned error: %v", err)
	}
	if leaderPlan != originalPlan {
		t.Fatalf("expected leader to keep original OpenClaw plan")
	}
	if planner.calls != 0 {
		t.Fatalf("expected leader plan to skip filtering, got %d calls", planner.calls)
	}

	workerPlan, err := service.openClawConfigPlanForTeamMember(7, plannedTeamMember{
		IsLeader: false,
		Request:  CreateTeamMemberRequest{OpenClawConfigPlan: originalPlan},
	})
	if err != nil {
		t.Fatalf("worker plan returned error: %v", err)
	}
	if workerPlan != filteredPlan {
		t.Fatalf("expected worker to use filtered OpenClaw plan")
	}
	if planner.calls != 1 || planner.userID != 7 || planner.plan != originalPlan {
		t.Fatalf("unexpected planner call: %#v", planner)
	}
}

func TestNewRedisBusParsesURLWithoutNetwork(t *testing.T) {
	bus, err := newRedisBus("redis://:pass@redis.example:6380/3")
	if err != nil {
		t.Fatalf("newRedisBus returned error: %v", err)
	}
	if bus.address != "redis.example:6380" || bus.password != "pass" || bus.db != 3 || bus.useTLS {
		t.Fatalf("unexpected redis bus config: %#v", bus)
	}
}

func TestDefaultTeamRedisURLUsesClusterServiceFallback(t *testing.T) {
	t.Setenv("CLAWMANAGER_TEAM_REDIS_URL", "")
	t.Setenv("TEAM_REDIS_URL", "")
	t.Setenv("REDIS_URL", "")
	t.Setenv("CLAWMANAGER_SYSTEM_NAMESPACE", "")
	t.Setenv("K8S_NAMESPACE", "clawmanager")
	t.Setenv("CLAWMANAGER_TEAM_REDIS_SERVICE_NAME", "")
	t.Setenv("CLAWMANAGER_TEAM_REDIS_SERVICE", "")
	t.Setenv("CLAWMANAGER_TEAM_REDIS_SERVICE_PORT", "")
	t.Setenv("CLAWMANAGER_TEAM_REDIS_PORT", "")
	t.Setenv("CLAWMANAGER_TEAM_REDIS_DB", "")
	t.Setenv("TEAM_REDIS_DB", "")

	got := defaultTeamRedisURL()
	want := "redis://clawmanager-team-redis.clawmanager-system.svc.cluster.local:6379/0"
	if got != want {
		t.Fatalf("expected default Team redis URL %q, got %q", want, got)
	}
}

func TestPlanTeamMembersRequiresExactlyOneLeader(t *testing.T) {
	_, err := planTeamMembers("team", []CreateTeamMemberRequest{
		{MemberID: "worker", Role: "developer"},
	})
	if err == nil || !strings.Contains(err.Error(), "exactly one leader") {
		t.Fatalf("expected exactly one leader validation error, got %v", err)
	}

	plans, err := planTeamMembers("team", []CreateTeamMemberRequest{
		{MemberID: "lead", Role: "team leader"},
		{MemberID: "worker", Role: "developer"},
	})
	if err != nil {
		t.Fatalf("planTeamMembers returned error: %v", err)
	}
	if len(plans) != 2 || !plans[0].IsLeader || plans[0].Role != "leader" {
		t.Fatalf("expected first member to be normalized as leader, got %#v", plans)
	}
	if plans[1].RuntimeType != "openclaw" {
		t.Fatalf("expected default runtime type openclaw, got %#v", plans[1])
	}
}

func TestPlanTeamMembersSupportsHermesRuntime(t *testing.T) {
	plans, err := planTeamMembers("team", []CreateTeamMemberRequest{
		{MemberID: "lead", Role: "leader"},
		{MemberID: "hermes-writer", Role: "writer", RuntimeType: "Hermes", InstanceMode: "Pro"},
	})
	if err != nil {
		t.Fatalf("planTeamMembers returned error: %v", err)
	}
	if plans[1].RuntimeType != "hermes" {
		t.Fatalf("expected Hermes runtime to be normalized, got %#v", plans[1])
	}
	if plans[1].InstanceMode != InstanceModePro {
		t.Fatalf("expected Pro instance mode to be normalized, got %#v", plans[1])
	}

	_, err = planTeamMembers("team", []CreateTeamMemberRequest{
		{MemberID: "lead", Role: "leader"},
		{MemberID: "worker", Role: "developer", RuntimeType: "ubuntu"},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported team member runtime type") {
		t.Fatalf("expected unsupported runtime validation error, got %v", err)
	}

	_, err = planTeamMembers("team", []CreateTeamMemberRequest{
		{MemberID: "lead", Role: "leader"},
		{MemberID: "worker", Role: "developer", Mode: "mini"},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported team member instance mode") {
		t.Fatalf("expected unsupported instance mode validation error, got %v", err)
	}
}

func TestTeamMemberInstanceNameUsesTeamIDAndMemberKey(t *testing.T) {
	name := teamMemberInstanceName("Software Engineering Team", 42, "code-reviewer")
	if name != "software-engineering-team-42-code-reviewer" {
		t.Fatalf("unexpected Team member instance name: %q", name)
	}

	longName := teamMemberInstanceName("very-long-software-engineering-platform-team", 12345, "extremely-long-code-reviewer-member-key")
	if len(longName) > 50 {
		t.Fatalf("expected instance name to stay within 50 chars, got %d: %q", len(longName), longName)
	}
	if !strings.Contains(longName, "-12345-") {
		t.Fatalf("expected instance name to include Team ID, got %q", longName)
	}
}

func TestBuildTeamRosterConfigOmitsSecrets(t *testing.T) {
	description := "reviews implementation and validates results"
	plans, err := planTeamMembers("team", []CreateTeamMemberRequest{
		{MemberID: "leader", Role: "leader"},
		{MemberID: "worker", Role: "developer", Description: &description},
	})
	if err != nil {
		t.Fatalf("planTeamMembers returned error: %v", err)
	}
	roster, err := buildTeamRosterConfig(&models.Team{
		ID:                9,
		CommunicationMode: "leader_mediated",
		SharedMountPath:   "/team",
	}, plans)
	if err != nil {
		t.Fatalf("buildTeamRosterConfig returned error: %v", err)
	}
	for _, forbidden := range []string{"REDIS_URL", "TOKEN", "OPENAI_API_KEY", "secret"} {
		if strings.Contains(roster, forbidden) {
			t.Fatalf("roster must not contain sensitive value marker %q: %s", forbidden, roster)
		}
	}
	if !strings.Contains(roster, `"leaderMemberId":"leader"`) || !strings.Contains(roster, `"eventsKey":"claw:team:9:events"`) {
		t.Fatalf("roster missing expected leader or redis keys: %s", roster)
	}
	if !strings.Contains(roster, description) {
		t.Fatalf("roster missing member description: %s", roster)
	}
	if !strings.Contains(roster, `"runtimeType":"openclaw"`) {
		t.Fatalf("roster missing member runtime type: %s", roster)
	}
	if !strings.Contains(roster, `"instanceMode":"lite"`) {
		t.Fatalf("roster missing member instance mode: %s", roster)
	}
}

func TestBuildInitialLeaderTaskPayloadDescribesRosterAndTeamSend(t *testing.T) {
	payload := buildInitialLeaderTaskPayload("Software Engineering Team")

	if payload["intent"] != initialLeaderTaskIntent {
		t.Fatalf("unexpected bootstrap intent: %#v", payload)
	}
	if payload["title"] == "" {
		t.Fatalf("expected bootstrap task title: %#v", payload)
	}
	prompt, ok := payload["prompt"].(string)
	if !ok {
		t.Fatalf("expected prompt string: %#v", payload)
	}
	for _, expected := range []string{
		"`team Software Engineering Team`",
		"Redis Team成员构成",
		"运行状态与技术能力边界",
		"协作与通信机制(team_send)",
		"任务流转方式",
		"消息同步方式",
		"上下文共享方式",
		"可调用的方法、工具与操作能力",
	} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("bootstrap prompt missing %q: %s", expected, prompt)
		}
	}
}

func TestBuildTeamTaskEnvelopeIncludesCompletionContract(t *testing.T) {
	task := &models.TeamTask{ID: 67}
	payload := map[string]interface{}{
		"intent": "team_bootstrap_introduction",
		"title":  "Introduce the team",
		"prompt": "Generate the team report.",
	}

	envelope := buildTeamTaskEnvelope(31, "leader", task, "team-31-bootstrap-introduction", payload, time.Unix(123, 0).UTC())

	if envelope["replyTo"] != "clawmanager" {
		t.Fatalf("expected replyTo clawmanager, got %#v", envelope["replyTo"])
	}
	if envelope["requiresCompletion"] != true {
		t.Fatalf("expected requiresCompletion=true, got %#v", envelope["requiresCompletion"])
	}
	if envelope["completionTool"] != "team_complete_task" {
		t.Fatalf("expected completion tool team_complete_task, got %#v", envelope["completionTool"])
	}
	resultSink, ok := envelope["resultSink"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected resultSink map, got %#v", envelope["resultSink"])
	}
	if resultSink["type"] != "redis_stream" || resultSink["eventsKey"] != "claw:team:31:events" {
		t.Fatalf("unexpected resultSink: %#v", resultSink)
	}
	if resultSink["successEvent"] != "task_completed" || resultSink["failureEvent"] != "task_failed" {
		t.Fatalf("unexpected resultSink events: %#v", resultSink)
	}
	prompt, ok := envelope["prompt"].(string)
	if !ok {
		t.Fatalf("expected prompt string, got %#v", envelope["prompt"])
	}
	for _, expected := range []string{"Generate the team report.", "team_complete_task", "resultMarkdown", "task_completed"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("completion prompt missing %q: %s", expected, prompt)
		}
	}
}

func TestProjectTeamEventTreatsFinalReplyAsTaskCompleted(t *testing.T) {
	taskID := 67
	messageID := "team-31-bootstrap-introduction"
	task := &models.TeamTask{
		ID:             taskID,
		TeamID:         31,
		TargetMemberID: 120,
		MessageID:      messageID,
		Status:         models.TeamTaskStatusRunning,
		UpdatedAt:      time.Now().UTC(),
	}
	member := &models.TeamMember{
		ID:            120,
		TeamID:        31,
		MemberKey:     "leader",
		Status:        models.TeamMemberStatusBusy,
		CurrentTaskID: &taskID,
		Availability:  models.TeamMemberAvailabilityBusy,
	}
	repo := &teamRepositoryStub{
		tasksByMessageID: map[string]*models.TeamTask{messageID: task},
		membersByKey:     map[string]*models.TeamMember{"leader": member},
	}
	service := &teamService{repo: repo}
	payload := map[string]interface{}{
		"event":          "reply",
		"messageId":      messageID,
		"memberId":       "leader",
		"taskId":         "team-31-task-67",
		"final":          true,
		"summary":        "Team report ready",
		"resultMarkdown": "Full report",
		"text":           "Full report",
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	err = service.projectTeamEvent(&models.Team{ID: 31}, nil, redisStreamMessage{
		ID:     "1781171178655-0",
		Fields: map[string]string{"payload": string(payloadJSON)},
	})
	if err != nil {
		t.Fatalf("projectTeamEvent returned error: %v", err)
	}

	if repo.updatedTask == nil || repo.updatedTask.Status != models.TeamTaskStatusSucceeded {
		t.Fatalf("expected final reply to mark task succeeded, got %#v", repo.updatedTask)
	}
	if repo.updatedTask.ResultJSON == nil || !strings.Contains(*repo.updatedTask.ResultJSON, "Full report") {
		t.Fatalf("expected reply payload to become task result, got %#v", repo.updatedTask.ResultJSON)
	}
	if repo.updatedMember == nil || repo.updatedMember.Status != models.TeamMemberStatusIdle || repo.updatedMember.Availability != models.TeamMemberAvailabilityIdle {
		t.Fatalf("expected member to become idle, got %#v", repo.updatedMember)
	}
	if len(repo.createdEvents) != 1 || repo.createdEvents[0].EventType != "task_completed" {
		t.Fatalf("expected stored event type task_completed, got %#v", repo.createdEvents)
	}
}

func TestActiveTeamMembersFiltersDeletedMembers(t *testing.T) {
	members := activeTeamMembers([]models.TeamMember{
		{MemberKey: "leader", Status: models.TeamMemberStatusIdle},
		{MemberKey: "old", Status: models.TeamMemberStatusDeleted},
		{MemberKey: "gone", Status: models.TeamMemberStatusDeleting},
	})
	if len(members) != 1 || members[0].MemberKey != "leader" {
		t.Fatalf("unexpected active members: %#v", members)
	}
}

func TestDeletedTeamNameReleasesUniqueName(t *testing.T) {
	name := deletedTeamName("DeepResearch", 42)
	if name != "DeepResearch__deleted_42" {
		t.Fatalf("unexpected deleted Team name: %q", name)
	}
	if again := deletedTeamName(name, 42); again != name {
		t.Fatalf("deleted Team name should be idempotent, got %q", again)
	}
}

func TestTeamTaskStaleTimeoutUsesEnvironment(t *testing.T) {
	t.Setenv("CLAWMANAGER_TEAM_TASK_STALE_SECONDS", "60")
	if got := teamTaskStaleTimeout(); got != time.Minute {
		t.Fatalf("expected one minute stale timeout, got %s", got)
	}

	t.Setenv("CLAWMANAGER_TEAM_TASK_STALE_SECONDS", "0")
	if got := teamTaskStaleTimeout(); got != 0 {
		t.Fatalf("expected disabled stale timeout, got %s", got)
	}
}

func TestApplyTeamMemberRuntimeProjectionSetsBlockedAvailability(t *testing.T) {
	member := &models.TeamMember{Availability: models.TeamMemberAvailabilityBusy}
	payload := map[string]interface{}{
		"availability":  "blocked",
		"lastSummary":   "Task failed: LLM request failed: network connection error.",
		"currentTaskId": "task_cb1062da-dff2-46ff-836f-86490583d944",
		"currentIntent": "weather_query_beijing",
	}

	applyTeamMemberRuntimeProjection(member, payload, "status")

	if member.Availability != models.TeamMemberAvailabilityBlocked {
		t.Fatalf("expected blocked availability, got %q", member.Availability)
	}
	if member.LastSummary == nil || !strings.Contains(*member.LastSummary, "LLM request failed") {
		t.Fatalf("expected last summary projection, got %#v", member.LastSummary)
	}
	if member.RuntimeTaskID == nil || *member.RuntimeTaskID != "task_cb1062da-dff2-46ff-836f-86490583d944" {
		t.Fatalf("expected runtime task id projection, got %#v", member.RuntimeTaskID)
	}
}

func TestApplyTeamMemberRuntimeProjectionClearsStaleBlockedOnCompletion(t *testing.T) {
	reason := "previous task failed"
	member := &models.TeamMember{
		Availability:  models.TeamMemberAvailabilityBlocked,
		BlockedReason: &reason,
	}
	payload := map[string]interface{}{
		"lastSummary":   "Redis Team task processing completed",
		"currentTaskId": "task_001",
	}

	applyTeamMemberRuntimeProjection(member, payload, "task_completed")

	if member.Availability != models.TeamMemberAvailabilityIdle {
		t.Fatalf("expected idle availability after task completion, got %q", member.Availability)
	}
	if member.BlockedReason != nil {
		t.Fatalf("expected stale blocked reason to be cleared, got %#v", *member.BlockedReason)
	}
}

func TestMergeMissingEventFieldsEnrichesOutboundPayload(t *testing.T) {
	base := map[string]interface{}{
		"event":     "outbound",
		"messageId": "msg_123",
		"from":      "leader",
		"to":        "worker",
	}
	extra := map[string]interface{}{
		"messageId": "msg_123",
		"title":     "Check date",
		"text":      "Check today's date and send the result back.",
		"metadata": map[string]interface{}{
			"prompt": "metadata prompt should also be available",
		},
	}

	merged := mergeMissingEventFields(base, extra)

	if merged["messageId"] != "msg_123" || merged["from"] != "leader" || merged["to"] != "worker" {
		t.Fatalf("base fields should be preserved, got %#v", merged)
	}
	if merged["title"] != "Check date" || merged["text"] == "" {
		t.Fatalf("expected outbound payload to be enriched with title/text, got %#v", merged)
	}
	if merged["prompt"] != "metadata prompt should also be available" {
		t.Fatalf("expected metadata prompt to be merged, got %#v", merged)
	}
	if !teamEventHasBody(merged) {
		t.Fatalf("expected enriched event to have displayable body: %#v", merged)
	}
}

type teamRepositoryStub struct {
	teamsByID        map[int]*models.Team
	membersByID      map[int]*models.TeamMember
	membersByKey     map[string]*models.TeamMember
	tasksByID        map[int]*models.TeamTask
	tasksByMessageID map[string]*models.TeamTask
	createdEvents    []models.TeamEvent
	updatedTask      *models.TeamTask
	updatedMember    *models.TeamMember
	updatedTeam      *models.Team
}

type teamOpenClawConfigPlannerStub struct {
	calls    int
	userID   int
	plan     *OpenClawConfigPlan
	nextPlan *OpenClawConfigPlan
	err      error
}

func (s *teamOpenClawConfigPlannerStub) PlanWithoutTeamMemberLeaderOnlyChannels(userID int, plan *OpenClawConfigPlan) (*OpenClawConfigPlan, error) {
	s.calls++
	s.userID = userID
	s.plan = plan
	return s.nextPlan, s.err
}

func (s *teamRepositoryStub) CreateTeam(team *models.Team) error { return nil }
func (s *teamRepositoryStub) UpdateTeam(team *models.Team) error {
	clone := *team
	s.updatedTeam = &clone
	return nil
}
func (s *teamRepositoryStub) GetTeamByID(id int) (*models.Team, error) {
	if s.teamsByID != nil {
		return s.teamsByID[id], nil
	}
	return nil, nil
}
func (s *teamRepositoryStub) GetTeamByUserIDAndName(userID int, name string) (*models.Team, error) {
	return nil, nil
}
func (s *teamRepositoryStub) ExistsByUserIDAndName(userID int, name string) (bool, error) {
	return false, nil
}
func (s *teamRepositoryStub) ListTeamsByUserID(userID int, offset, limit int) ([]models.Team, error) {
	return nil, nil
}
func (s *teamRepositoryStub) ListActiveTeams() ([]models.Team, error) { return nil, nil }
func (s *teamRepositoryStub) CountTeamsByUserID(userID int) (int, error) {
	return 0, nil
}
func (s *teamRepositoryStub) CreateMember(member *models.TeamMember) error { return nil }
func (s *teamRepositoryStub) UpdateMember(member *models.TeamMember) error {
	clone := *member
	s.updatedMember = &clone
	return nil
}
func (s *teamRepositoryStub) GetMemberByID(id int) (*models.TeamMember, error) {
	if s.membersByID != nil {
		return s.membersByID[id], nil
	}
	return nil, nil
}
func (s *teamRepositoryStub) GetMemberByTeamKey(teamID int, memberKey string) (*models.TeamMember, error) {
	if s.membersByKey == nil {
		return nil, nil
	}
	member := s.membersByKey[memberKey]
	if member == nil || member.TeamID != teamID {
		return nil, nil
	}
	return member, nil
}
func (s *teamRepositoryStub) ListMembersByTeamID(teamID int) ([]models.TeamMember, error) {
	return nil, nil
}
func (s *teamRepositoryStub) CreateTask(task *models.TeamTask) error { return nil }
func (s *teamRepositoryStub) UpdateTask(task *models.TeamTask) error {
	clone := *task
	s.updatedTask = &clone
	return nil
}
func (s *teamRepositoryStub) GetTaskByID(id int) (*models.TeamTask, error) {
	if s.tasksByID != nil {
		return s.tasksByID[id], nil
	}
	return nil, nil
}
func (s *teamRepositoryStub) GetTaskByMessageID(teamID int, messageID string) (*models.TeamTask, error) {
	if s.tasksByMessageID == nil {
		return nil, nil
	}
	task := s.tasksByMessageID[messageID]
	if task == nil || task.TeamID != teamID {
		return nil, nil
	}
	return task, nil
}
func (s *teamRepositoryStub) ListTasksByTeamID(teamID int, limit int) ([]models.TeamTask, error) {
	return nil, nil
}
func (s *teamRepositoryStub) ListTasksBeforeID(teamID, beforeID, limit int) ([]models.TeamTask, error) {
	return nil, nil
}
func (s *teamRepositoryStub) ListStaleCandidateTasks(cutoff time.Time, limit int) ([]models.TeamTask, error) {
	return nil, nil
}
func (s *teamRepositoryStub) CreateEvent(event *models.TeamEvent) error {
	clone := *event
	s.createdEvents = append(s.createdEvents, clone)
	return nil
}
func (s *teamRepositoryStub) EventExistsByStreamID(teamID int, streamID string) (bool, error) {
	return false, nil
}
func (s *teamRepositoryStub) ListEventsByTeamID(teamID int, limit int) ([]models.TeamEvent, error) {
	return nil, nil
}
func (s *teamRepositoryStub) ListEventsBeforeID(teamID, beforeID, limit int) ([]models.TeamEvent, error) {
	return nil, nil
}
