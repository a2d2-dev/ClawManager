import type { OpenClawConfigPlan } from "./openclawConfig";

export interface Team {
  id: number;
  user_id: number;
  name: string;
  description?: string;
  status: "creating" | "running" | "failed" | "deleting" | "deleted";
  communication_mode: string;
  redis_events_last_id: string;
  shared_pvc_name?: string;
  shared_pvc_namespace?: string;
  shared_mount_path: string;
  storage_class?: string;
  created_at: string;
  updated_at: string;
}

export interface TeamMember {
  id: number;
  team_id: number;
  instance_id?: number;
  user_id: number;
  member_key: string;
  display_name: string;
  role: string;
  runtime_type?: "openclaw" | "hermes";
  description?: string;
  status:
    | "creating"
    | "idle"
    | "busy"
    | "failed"
    | "offline"
    | "deleting"
    | "deleted";
  current_task_id?: number;
  progress: number;
  last_seen_at?: string;
  availability?: "unknown" | "idle" | "busy" | "blocked" | "offline";
  runtime_status?: string;
  runtime_task_id?: string;
  runtime_intent?: string;
  blocked_reason?: string;
  last_summary?: string;
  created_at: string;
  updated_at: string;
}

export interface TeamTask {
  id: number;
  team_id: number;
  target_member_id: number;
  created_by?: number;
  message_id: string;
  status:
    | "pending"
    | "dispatched"
    | "running"
    | "succeeded"
    | "failed"
    | "stale";
  redis_stream_id?: string;
  error_message?: string;
  created_at: string;
  dispatched_at?: string;
  started_at?: string;
  finished_at?: string;
  updated_at: string;
  payload?: Record<string, unknown>;
  result?: Record<string, unknown>;
}

export interface TeamEvent {
  id: number;
  team_id: number;
  member_id?: number;
  task_id?: number;
  message_id?: string;
  event_type: string;
  redis_stream_id?: string;
  occurred_at?: string;
  created_at: string;
  payload?: Record<string, unknown>;
}

export interface CreateTeamMemberRequest {
  member_id?: string;
  name?: string;
  role: string;
  runtime_type?: "openclaw" | "hermes";
  description?: string;
  cpu_cores?: number;
  memory_gb?: number;
  disk_gb?: number;
  gpu_enabled?: boolean;
  gpu_count?: number;
  image_registry?: string;
  image_tag?: string;
  environment_overrides?: Record<string, string>;
  openclaw_config_plan?: OpenClawConfigPlan;
  is_leader?: boolean;
}

export interface CreateTeamRequest {
  name: string;
  description?: string;
  communication_mode?: string;
  shared_storage_gb?: number;
  storage_class?: string;
  members: CreateTeamMemberRequest[];
}

export interface TeamDetails {
  team: Team;
  leader_member_id?: string;
  leader?: TeamMember;
  members: TeamMember[];
  tasks?: TeamTask[];
  events?: TeamEvent[];
}

export interface TeamTasksHistoryResponse {
  tasks: TeamTask[];
  has_more: boolean;
  next_before_id?: number;
}

export interface TeamEventsHistoryResponse {
  events: TeamEvent[];
  has_more: boolean;
  next_before_id?: number;
}

export interface TeamListResponse {
  teams: Team[];
  total: number;
  page: number;
  limit: number;
}

export interface DispatchTeamTaskRequest {
  target_member_id: string;
  message_id?: string;
  payload: Record<string, unknown>;
}
