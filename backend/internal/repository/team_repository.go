package repository

import (
	"fmt"
	"strings"
	"time"

	"clawreef/internal/models"

	"github.com/upper/db/v4"
)

type TeamRepository interface {
	CreateTeam(team *models.Team) error
	UpdateTeam(team *models.Team) error
	GetTeamByID(id int) (*models.Team, error)
	GetTeamByUserIDAndName(userID int, name string) (*models.Team, error)
	ExistsByUserIDAndName(userID int, name string) (bool, error)
	ListTeamsByUserID(userID int, offset, limit int) ([]models.Team, error)
	ListActiveTeams() ([]models.Team, error)
	CountTeamsByUserID(userID int) (int, error)

	CreateMember(member *models.TeamMember) error
	UpdateMember(member *models.TeamMember) error
	GetMemberByID(id int) (*models.TeamMember, error)
	GetMemberByTeamKey(teamID int, memberKey string) (*models.TeamMember, error)
	ListMembersByTeamID(teamID int) ([]models.TeamMember, error)

	CreateTask(task *models.TeamTask) error
	UpdateTask(task *models.TeamTask) error
	GetTaskByID(id int) (*models.TeamTask, error)
	GetTaskByMessageID(teamID int, messageID string) (*models.TeamTask, error)
	ListTasksByTeamID(teamID int, limit int) ([]models.TeamTask, error)
	ListTasksBeforeID(teamID, beforeID, limit int) ([]models.TeamTask, error)
	ListStaleCandidateTasks(cutoff time.Time, limit int) ([]models.TeamTask, error)

	CreateEvent(event *models.TeamEvent) error
	EventExistsByStreamID(teamID int, streamID string) (bool, error)
	ListEventsByTeamID(teamID int, limit int) ([]models.TeamEvent, error)
	ListEventsBeforeID(teamID, beforeID, limit int) ([]models.TeamEvent, error)
}

type teamRepository struct {
	sess db.Session
}

func NewTeamRepository(sess db.Session) TeamRepository {
	return &teamRepository{sess: sess}
}

func (r *teamRepository) CreateTeam(team *models.Team) error {
	ensureTimestamps(&team.CreatedAt, &team.UpdatedAt)
	res, err := r.sess.Collection("teams").Insert(team)
	if err != nil {
		if strings.Contains(err.Error(), "Duplicate entry") && strings.Contains(err.Error(), "uk_teams_user_name") {
			return fmt.Errorf("team name already exists")
		}
		return fmt.Errorf("failed to create team: %w", err)
	}
	if id, ok := res.ID().(int64); ok {
		team.ID = int(id)
	}
	return nil
}

func (r *teamRepository) UpdateTeam(team *models.Team) error {
	if team.UpdatedAt.IsZero() {
		team.UpdatedAt = time.Now().UTC()
	}
	if err := r.sess.Collection("teams").Find(db.Cond{"id": team.ID}).Update(team); err != nil {
		return fmt.Errorf("failed to update team: %w", err)
	}
	return nil
}

func (r *teamRepository) GetTeamByID(id int) (*models.Team, error) {
	var team models.Team
	if err := r.sess.Collection("teams").Find(db.Cond{"id": id}).One(&team); err != nil {
		if err == db.ErrNoMoreRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get team: %w", err)
	}
	return &team, nil
}

func (r *teamRepository) GetTeamByUserIDAndName(userID int, name string) (*models.Team, error) {
	var team models.Team
	if err := r.sess.Collection("teams").Find(db.Cond{"user_id": userID, "name": name}).One(&team); err != nil {
		if err == db.ErrNoMoreRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get team by name: %w", err)
	}
	return &team, nil
}

func (r *teamRepository) ExistsByUserIDAndName(userID int, name string) (bool, error) {
	count, err := r.sess.Collection("teams").Find(db.Cond{"user_id": userID, "name": name}).Count()
	if err != nil {
		return false, fmt.Errorf("failed to check team name: %w", err)
	}
	return count > 0, nil
}

func (r *teamRepository) ListTeamsByUserID(userID int, offset, limit int) ([]models.Team, error) {
	if limit <= 0 {
		limit = 20
	}
	var teams []models.Team
	if err := r.sess.Collection("teams").Find(db.Cond{"user_id": userID}).OrderBy("-created_at", "-id").Offset(offset).Limit(limit).All(&teams); err != nil {
		return nil, fmt.Errorf("failed to list teams: %w", err)
	}
	return teams, nil
}

func (r *teamRepository) ListActiveTeams() ([]models.Team, error) {
	var teams []models.Team
	if err := r.sess.Collection("teams").Find(db.Cond{"status IN": []string{models.TeamStatusCreating, models.TeamStatusRunning}}).All(&teams); err != nil {
		return nil, fmt.Errorf("failed to list active teams: %w", err)
	}
	return teams, nil
}

func (r *teamRepository) CountTeamsByUserID(userID int) (int, error) {
	count, err := r.sess.Collection("teams").Find(db.Cond{"user_id": userID}).Count()
	if err != nil {
		return 0, fmt.Errorf("failed to count teams: %w", err)
	}
	return int(count), nil
}

func (r *teamRepository) CreateMember(member *models.TeamMember) error {
	ensureTimestamps(&member.CreatedAt, &member.UpdatedAt)
	res, err := r.sess.Collection("team_members").Insert(member)
	if err != nil {
		return fmt.Errorf("failed to create team member: %w", err)
	}
	if id, ok := res.ID().(int64); ok {
		member.ID = int(id)
	}
	return nil
}

func (r *teamRepository) UpdateMember(member *models.TeamMember) error {
	if member.UpdatedAt.IsZero() {
		member.UpdatedAt = time.Now().UTC()
	}
	if err := r.sess.Collection("team_members").Find(db.Cond{"id": member.ID}).Update(member); err != nil {
		return fmt.Errorf("failed to update team member: %w", err)
	}
	return nil
}

func (r *teamRepository) GetMemberByID(id int) (*models.TeamMember, error) {
	var member models.TeamMember
	if err := r.sess.Collection("team_members").Find(db.Cond{"id": id}).One(&member); err != nil {
		if err == db.ErrNoMoreRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get team member: %w", err)
	}
	return &member, nil
}

func (r *teamRepository) GetMemberByTeamKey(teamID int, memberKey string) (*models.TeamMember, error) {
	var member models.TeamMember
	if err := r.sess.Collection("team_members").Find(db.Cond{"team_id": teamID, "member_key": memberKey}).One(&member); err != nil {
		if err == db.ErrNoMoreRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get team member by key: %w", err)
	}
	return &member, nil
}

func (r *teamRepository) ListMembersByTeamID(teamID int) ([]models.TeamMember, error) {
	var members []models.TeamMember
	if err := r.sess.Collection("team_members").Find(db.Cond{"team_id": teamID}).OrderBy("id").All(&members); err != nil {
		return nil, fmt.Errorf("failed to list team members: %w", err)
	}
	return members, nil
}

func (r *teamRepository) CreateTask(task *models.TeamTask) error {
	ensureTimestamps(&task.CreatedAt, &task.UpdatedAt)
	res, err := r.sess.Collection("team_tasks").Insert(task)
	if err != nil {
		return fmt.Errorf("failed to create team task: %w", err)
	}
	if id, ok := res.ID().(int64); ok {
		task.ID = int(id)
	}
	return nil
}

func (r *teamRepository) UpdateTask(task *models.TeamTask) error {
	if task.UpdatedAt.IsZero() {
		task.UpdatedAt = time.Now().UTC()
	}
	if err := r.sess.Collection("team_tasks").Find(db.Cond{"id": task.ID}).Update(task); err != nil {
		return fmt.Errorf("failed to update team task: %w", err)
	}
	return nil
}

func (r *teamRepository) GetTaskByID(id int) (*models.TeamTask, error) {
	var task models.TeamTask
	if err := r.sess.Collection("team_tasks").Find(db.Cond{"id": id}).One(&task); err != nil {
		if err == db.ErrNoMoreRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get team task: %w", err)
	}
	return &task, nil
}

func (r *teamRepository) GetTaskByMessageID(teamID int, messageID string) (*models.TeamTask, error) {
	var task models.TeamTask
	if err := r.sess.Collection("team_tasks").Find(db.Cond{"team_id": teamID, "message_id": messageID}).One(&task); err != nil {
		if err == db.ErrNoMoreRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get team task by message id: %w", err)
	}
	return &task, nil
}

func (r *teamRepository) ListTasksByTeamID(teamID int, limit int) ([]models.TeamTask, error) {
	if limit <= 0 {
		limit = 20
	}
	var tasks []models.TeamTask
	if err := r.sess.Collection("team_tasks").Find(db.Cond{"team_id": teamID}).OrderBy("-created_at", "-id").Limit(limit).All(&tasks); err != nil {
		return nil, fmt.Errorf("failed to list team tasks: %w", err)
	}
	return tasks, nil
}

func (r *teamRepository) ListTasksBeforeID(teamID, beforeID, limit int) ([]models.TeamTask, error) {
	if limit <= 0 {
		limit = 20
	}
	cond := db.Cond{"team_id": teamID}
	if beforeID > 0 {
		cond["id <"] = beforeID
	}
	var tasks []models.TeamTask
	if err := r.sess.Collection("team_tasks").Find(cond).OrderBy("-created_at", "-id").Limit(limit).All(&tasks); err != nil {
		return nil, fmt.Errorf("failed to list team task history: %w", err)
	}
	return tasks, nil
}

func (r *teamRepository) ListStaleCandidateTasks(cutoff time.Time, limit int) ([]models.TeamTask, error) {
	if limit <= 0 {
		limit = 100
	}
	var tasks []models.TeamTask
	statuses := []string{
		models.TeamTaskStatusDispatched,
		models.TeamTaskStatusRunning,
	}
	if err := r.sess.Collection("team_tasks").Find(db.Cond{
		"status IN":    statuses,
		"updated_at <": cutoff,
	}).OrderBy("updated_at", "id").Limit(limit).All(&tasks); err != nil {
		return nil, fmt.Errorf("failed to list stale candidate team tasks: %w", err)
	}
	return tasks, nil
}

func (r *teamRepository) CreateEvent(event *models.TeamEvent) error {
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	res, err := r.sess.Collection("team_events").Insert(event)
	if err != nil {
		return fmt.Errorf("failed to create team event: %w", err)
	}
	if id, ok := res.ID().(int64); ok {
		event.ID = int(id)
	}
	return nil
}

func (r *teamRepository) EventExistsByStreamID(teamID int, streamID string) (bool, error) {
	if streamID == "" {
		return false, nil
	}
	count, err := r.sess.Collection("team_events").Find(db.Cond{"team_id": teamID, "redis_stream_id": streamID}).Count()
	if err != nil {
		return false, fmt.Errorf("failed to check team event stream id: %w", err)
	}
	return count > 0, nil
}

func (r *teamRepository) ListEventsByTeamID(teamID int, limit int) ([]models.TeamEvent, error) {
	if limit <= 0 {
		limit = 50
	}
	var events []models.TeamEvent
	if err := r.sess.Collection("team_events").Find(db.Cond{"team_id": teamID}).OrderBy("-created_at", "-id").Limit(limit).All(&events); err != nil {
		return nil, fmt.Errorf("failed to list team events: %w", err)
	}
	return events, nil
}

func (r *teamRepository) ListEventsBeforeID(teamID, beforeID, limit int) ([]models.TeamEvent, error) {
	if limit <= 0 {
		limit = 50
	}
	cond := db.Cond{"team_id": teamID}
	if beforeID > 0 {
		cond["id <"] = beforeID
	}
	var events []models.TeamEvent
	if err := r.sess.Collection("team_events").Find(cond).OrderBy("-created_at", "-id").Limit(limit).All(&events); err != nil {
		return nil, fmt.Errorf("failed to list team event history: %w", err)
	}
	return events, nil
}
