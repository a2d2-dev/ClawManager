import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { InstanceAccess } from "../../components/InstanceAccess";
import UserLayout from "../../components/UserLayout";
import { useAuth } from "../../contexts/AuthContext";
import { instanceService } from "../../services/instanceService";
import { teamService } from "../../services/teamService";
import type { Instance } from "../../types/instance";
import type { TeamDetails, TeamEvent, TeamMember, TeamTask } from "../../types/team";

const statusStyle = (status: string) => {
  switch (status) {
    case "running":
    case "idle":
    case "succeeded":
      return "border-green-200 bg-green-50 text-green-700";
    case "busy":
    case "dispatched":
      return "border-blue-200 bg-blue-50 text-blue-700";
    case "creating":
    case "pending":
    case "stale":
      return "border-yellow-200 bg-yellow-50 text-yellow-700";
    case "failed":
      return "border-red-200 bg-red-50 text-red-700";
    case "offline":
      return "border-gray-200 bg-gray-50 text-gray-700";
    default:
      return "border-gray-200 bg-gray-50 text-gray-700";
  }
};

const availabilityStyle = (availability?: string) => {
  switch (availability) {
    case "idle":
      return "border-green-200 bg-green-50 text-green-700";
    case "busy":
      return "border-blue-200 bg-blue-50 text-blue-700";
    case "blocked":
      return "border-red-200 bg-red-50 text-red-700";
    case "offline":
      return "border-gray-200 bg-gray-50 text-gray-700";
    default:
      return "border-gray-200 bg-gray-50 text-gray-700";
  }
};

const formatDateTime = (value?: string) => {
  if (!value) {
    return "-";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return date.toLocaleString();
};

const compactJson = (value?: Record<string, unknown>) => {
  if (!value) {
    return "-";
  }
  try {
    return JSON.stringify(value);
  } catch {
    return "-";
  }
};

const asRecord = (value: unknown): Record<string, unknown> | undefined =>
  value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : undefined;

const parseJsonRecord = (value: unknown): Record<string, unknown> | undefined => {
  if (typeof value !== "string" || !value.trim().startsWith("{")) {
    return undefined;
  }
  try {
    return asRecord(JSON.parse(value));
  } catch {
    return undefined;
  }
};

const TEAM_TASK_HISTORY_PAGE_SIZE = 20;
const TEAM_EVENT_HISTORY_PAGE_SIZE = 50;

const mergeByIdDesc = <T extends { id: number }>(...groups: T[][]) => {
  const merged = new Map<number, T>();
  for (const group of groups) {
    for (const item of group) {
      merged.set(item.id, item);
    }
  }
  return [...merged.values()].sort((a, b) => b.id - a.id);
};

const oldestID = (items: { id: number }[]) =>
  items.reduce<number | undefined>(
    (current, item) => (current === undefined ? item.id : Math.min(current, item.id)),
    undefined,
  );

const normalizeEventPayload = (event: TeamEvent) => {
  const payload = event.payload || {};
  const embedded = parseJsonRecord(payload.payload);
  return embedded ? { ...embedded, ...payload } : payload;
};

const payloadText = (
  payload: Record<string, unknown> | undefined,
  keys: string[],
) => {
  if (!payload) {
    return "";
  }
  for (const key of keys) {
    const value = payload[key];
    if (typeof value === "string" && value.trim()) {
      return value.trim();
    }
    if (typeof value === "number" || typeof value === "boolean") {
      return String(value);
    }
  }
  return "";
};

const payloadRecordCandidates = (
  payload: Record<string, unknown> | undefined,
) => {
  if (!payload) {
    return [];
  }
  const records: Record<string, unknown>[] = [payload];
  for (const key of ["sent", "message", "metadata", "data", "envelope", "task"]) {
    const direct = asRecord(payload[key]);
    if (direct) {
      records.push(direct);
      continue;
    }
    const parsed = parseJsonRecord(payload[key]);
    if (parsed) {
      records.push(parsed);
    }
  }
  return records;
};

const payloadTextDeep = (
  payload: Record<string, unknown> | undefined,
  keys: string[],
) => {
  for (const record of payloadRecordCandidates(payload)) {
    const text = payloadText(record, keys);
    if (text) {
      return text;
    }
  }
  return "";
};

const payloadNumber = (
  payload: Record<string, unknown> | undefined,
  keys: string[],
) => {
  if (!payload) {
    return undefined;
  }
  for (const key of keys) {
    const value = payload[key];
    if (typeof value === "number" && Number.isFinite(value)) {
      return value;
    }
    if (typeof value === "string" && value.trim() && !Number.isNaN(Number(value))) {
      return Number(value);
    }
  }
  return undefined;
};

const taskTitleText = (task: TeamTask) =>
  payloadText(task.payload, ["title", "intent"]) || `任务 #${task.id}`;

const taskPromptText = (task: TeamTask) =>
  payloadText(task.payload, [
    "prompt",
    "instruction",
    "instructions",
    "goal",
    "query",
  ]);

const taskIntentText = (payload?: Record<string, unknown>) =>
  payloadText(payload, ["intent", "runtime_intent", "currentIntent"]);

const memberKeyFromEvent = (
  event: TeamEvent,
  memberById: Map<number, TeamMember>,
) =>
  event.member_id
    ? memberById.get(event.member_id)?.member_key || `#${event.member_id}`
    : payloadText(event.payload, ["memberId", "member_id", "to", "from"]) || "-";

const eventVerb = (eventType: string) => {
  switch (eventType) {
    case "outbound":
      return "发送/转派";
    case "reply":
      return "回复";
    case "progress":
      return "进度";
    case "completion":
      return "完成回执";
    case "task_received":
      return "收到任务";
    case "task_started":
      return "开始执行";
    case "task_progress":
      return "进度更新";
    case "task_assigned":
      return "任务转派";
    case "task_completed":
      return "完成任务";
    case "task_failed":
      return "任务失败";
    case "message_failed":
      return "消息失败";
    case "task_stale":
      return "长时间无进展";
    default:
      return eventType;
  }
};

const eventTone = (eventType: string) => {
  if (eventType === "task_completed" || eventType === "completion" || eventType === "reply") {
    return "border-green-200 bg-green-50 text-green-700";
  }
  if (eventType === "task_failed" || eventType === "message_failed" || eventType === "dlq") {
    return "border-red-200 bg-red-50 text-red-700";
  }
  if (eventType === "task_stale") {
    return "border-yellow-200 bg-yellow-50 text-yellow-700";
  }
  return "border-blue-200 bg-blue-50 text-blue-700";
};

type CollaborationItem = {
  event: TeamEvent;
  payload: Record<string, unknown>;
  eventType: string;
  actor: string;
  from: string;
  to: string;
  taskKey: string;
  taskLabel: string;
  content: string;
  occurredAt?: string;
  timeMs: number;
};

type CollaborationGroup = {
  key: string;
  label: string;
  title: string;
  status: string;
  route: string[];
  latestAt: number;
  task?: TeamTask;
  items: CollaborationItem[];
};

const eventTimeValue = (event: TeamEvent) =>
  event.occurred_at || event.created_at;

const eventTimeMs = (event: TeamEvent) => {
  const value = eventTimeValue(event);
  const ms = value ? new Date(value).getTime() : 0;
  return Number.isFinite(ms) ? ms : 0;
};

const collaborationEventType = (
  event: TeamEvent,
  payload: Record<string, unknown>,
) => payloadText(payload, ["event", "event_type", "type"]) || event.event_type;

const canonicalTaskKey = (taskId: number | string) => `clawmanager-task-${taskId}`;

const payloadTaskIDs = (payload: Record<string, unknown>) =>
  [
    "taskId",
    "task_id",
    "currentTaskId",
    "runtimeTaskId",
    "MessageThreadId",
  ]
    .map((key) => payloadText(payload, [key]))
    .filter(Boolean);

const taskCreatorKey = (task?: TeamTask) =>
  task?.created_by ? `user-${task.created_by}` : "user";

const taskKeyFromEvent = (
  event: TeamEvent,
  payload: Record<string, unknown>,
  taskKeyByEventTaskID: Map<string, string>,
  taskKeyByMessageID: Map<string, string>,
) => {
  if (event.task_id) {
    return canonicalTaskKey(event.task_id);
  }
  const messageId = payloadText(payload, ["messageId", "message_id"]) || event.message_id;
  if (messageId) {
    const taskKey = taskKeyByMessageID.get(messageId);
    if (taskKey) {
      return taskKey;
    }
  }
  for (const taskId of payloadTaskIDs(payload)) {
    const taskKey = taskKeyByEventTaskID.get(taskId);
    if (taskKey) {
      return taskKey;
    }
  }
  const taskId = payloadTaskIDs(payload)[0];
  if (taskId) {
    return taskId;
  }
  const inReplyTo = payloadText(payload, ["inReplyTo", "in_reply_to"]);
  if (inReplyTo) {
    const taskKey = taskKeyByMessageID.get(inReplyTo);
    if (taskKey) {
      return taskKey;
    }
    return `reply:${inReplyTo}`;
  }
  if (messageId) {
    return `message:${messageId}`;
  }
  return `event:${event.id}`;
};

const taskLabelFromKey = (key: string, event: TeamEvent) => {
  if (event.task_id) {
    return `ClawManager #${event.task_id}`;
  }
  if (key.startsWith("message:")) {
    return key.replace("message:", "message ");
  }
  if (key.startsWith("reply:")) {
    return key.replace("reply:", "reply to ");
  }
  if (key.startsWith("event:")) {
    return "未归类事件";
  }
  return key;
};

const collaborationContent = (
  payload: Record<string, unknown>,
) => {
  const resultMarkdown = payloadTextDeep(payload, ["resultMarkdown"]);
  if (resultMarkdown) {
    return resultMarkdown;
  }
  const title = payloadTextDeep(payload, ["title"]);
  const text = payloadTextDeep(payload, [
    "text",
    "prompt",
    "instruction",
    "instructions",
    "goal",
    "query",
  ]);
  if (title && text && !text.includes(title)) {
    return `**${title}**\n\n${text}`;
  }
  if (text) {
    return text;
  }
  return payloadTextDeep(payload, [
    "resultMarkdown",
    "summary",
    "lastSummary",
    "diagnostic",
    "error",
    "error_message",
    "message",
    "title",
  ]);
};

const routeFromItem = (item: CollaborationItem) =>
  [item.from, item.actor, item.to].filter((value, index, values) =>
    value && values.indexOf(value) === index,
  );

const eventActorKey = (
  event: TeamEvent,
  payload: Record<string, unknown>,
  eventType: string,
  from: string,
  memberById: Map<number, TeamMember>,
  task?: TeamTask,
) => {
  if ((eventType === "outbound" || eventType === "task_assigned") && task?.created_by) {
    return taskCreatorKey(task);
  }
  if (from && from !== "clawmanager") {
    return from;
  }
  if (from === "clawmanager" && task?.created_by) {
    return taskCreatorKey(task);
  }
  if (event.member_id) {
    return memberById.get(event.member_id)?.member_key || `#${event.member_id}`;
  }
  return payloadText(payload, ["memberId", "member_id", "from", "to"]) || "system";
};

const inferGroupStatus = (items: CollaborationItem[], task?: TeamTask) => {
  if (task?.status) {
    return task.status;
  }
  const latest = [...items].sort((a, b) => b.timeMs - a.timeMs)[0];
  const terminal = items.find((item) => {
    const status = payloadText(item.payload, ["status"]).toLowerCase();
    return (
      item.eventType === "task_failed" ||
      item.eventType === "message_failed" ||
      status === "failed" ||
      item.eventType === "task_completed" ||
      item.eventType === "completion" ||
      status === "succeeded"
    );
  });
  if (terminal) {
    const status = payloadText(terminal.payload, ["status"]).toLowerCase();
    if (
      terminal.eventType === "task_failed" ||
      terminal.eventType === "message_failed" ||
      status === "failed"
    ) {
      return "failed";
    }
    return "succeeded";
  }
  if (latest?.eventType === "reply") {
    return "replied";
  }
  if (items.some((item) => item.eventType === "progress" || item.eventType === "task_started")) {
    return "running";
  }
  if (items.some((item) => item.eventType === "outbound" || item.eventType === "task_assigned")) {
    return "dispatched";
  }
  return "observed";
};

const buildCollaborationGroups = (
  events: TeamEvent[],
  tasks: TeamTask[],
  memberById: Map<number, TeamMember>,
) => {
  const taskByID = new Map(tasks.map((task) => [task.id, task]));
  const taskByKey = new Map<string, TeamTask>();
  const taskKeyByEventTaskID = new Map<string, string>();
  const taskKeyByMessageID = new Map<string, string>();

  for (const task of tasks) {
    const taskKey = canonicalTaskKey(task.id);
    taskByKey.set(taskKey, task);
    taskKeyByEventTaskID.set(taskKey, taskKey);
    taskKeyByEventTaskID.set(String(task.id), taskKey);
    taskKeyByEventTaskID.set(`team-${task.team_id}-task-${task.id}`, taskKey);
    if (task.message_id) {
      taskKeyByMessageID.set(task.message_id, taskKey);
    }
  }

  for (const event of events) {
    const payload = normalizeEventPayload(event);
    const messageID = payloadText(payload, ["messageId", "message_id"]) || event.message_id;
    const canonicalKey =
      (event.task_id ? canonicalTaskKey(event.task_id) : "") ||
      (messageID && taskKeyByMessageID.get(messageID)) ||
      "";
    if (!canonicalKey) {
      continue;
    }
    for (const taskID of payloadTaskIDs(payload)) {
      taskKeyByEventTaskID.set(taskID, canonicalKey);
    }
    const inReplyTo = payloadText(payload, ["inReplyTo", "in_reply_to"]);
    if (messageID) {
      taskKeyByMessageID.set(messageID, canonicalKey);
    }
    if (inReplyTo) {
      taskKeyByMessageID.set(inReplyTo, canonicalKey);
    }
  }
  const groups = new Map<string, CollaborationGroup>();

  for (const event of events) {
    const payload = normalizeEventPayload(event);
    const eventType = collaborationEventType(event, payload);
    const from = payloadText(payload, ["from"]);
    const to = payloadText(payload, ["to", "recipient", "memberId"]);
    const taskKey = taskKeyFromEvent(
      event,
      payload,
      taskKeyByEventTaskID,
      taskKeyByMessageID,
    );
    const existingTask =
      taskByKey.get(taskKey) ||
      (event.task_id ? taskByID.get(event.task_id) : undefined);
    const actor = eventActorKey(event, payload, eventType, from, memberById, existingTask);
    const item: CollaborationItem = {
      event,
      payload,
      eventType,
      actor,
      from,
      to,
      taskKey,
      taskLabel: taskLabelFromKey(taskKey, event),
      content: collaborationContent(payload),
      occurredAt: eventTimeValue(event),
      timeMs: eventTimeMs(event),
    };
    const current = groups.get(taskKey);
    if (current) {
      current.items.push(item);
      current.latestAt = Math.max(current.latestAt, item.timeMs);
      current.route = [...current.route, ...routeFromItem(item)].filter(
        (value, index, values) => values.indexOf(value) === index,
      );
      if (!current.task && existingTask) {
        current.task = existingTask;
      }
    } else {
      groups.set(taskKey, {
        key: taskKey,
        label: item.taskLabel,
        title:
          payloadText(payload, ["title", "intent"]) ||
          (existingTask ? taskTitleText(existingTask) : item.taskLabel),
        status: "observed",
        route: routeFromItem(item),
        latestAt: item.timeMs,
        task: existingTask,
        items: [item],
      });
    }
  }

  for (const task of tasks) {
    const key = canonicalTaskKey(task.id);
    if (!groups.has(key)) {
      const target = memberById.get(task.target_member_id)?.member_key || `#${task.target_member_id}`;
      groups.set(key, {
        key,
        label: `ClawManager #${task.id}`,
        title: taskTitleText(task),
        status: task.status,
        route: ["ClawManager", target],
        latestAt: new Date(task.updated_at || task.created_at).getTime(),
        task,
        items: [],
      });
    }
  }

  return [...groups.values()]
    .map((group) => ({
      ...group,
      status: inferGroupStatus(group.items, group.task),
      items: [...group.items].sort((a, b) => a.timeMs - b.timeMs || a.event.id - b.event.id),
    }))
    .sort((a, b) => b.latestAt - a.latestAt);
};

const TeamDetailPage: React.FC = () => {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const { user } = useAuth();
  const teamId = id ? Number(id) : null;
  const [details, setDetails] = useState<TeamDetails | null>(null);
  const [loadedTasks, setLoadedTasks] = useState<TeamTask[]>([]);
  const [loadedEvents, setLoadedEvents] = useState<TeamEvent[]>([]);
  const [hasMoreTasks, setHasMoreTasks] = useState(false);
  const [hasMoreEvents, setHasMoreEvents] = useState(false);
  const taskHistoryExhausted = useRef(false);
  const eventHistoryExhausted = useRef(false);
  const [historyLoading, setHistoryLoading] = useState(false);
  const [historyError, setHistoryError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [targetMember, setTargetMember] = useState("");
  const [taskTitle, setTaskTitle] = useState("server-smoke");
  const [taskPrompt, setTaskPrompt] = useState("");
  const [dispatching, setDispatching] = useState(false);
  const [dispatchError, setDispatchError] = useState<string | null>(null);
  const [desktopMemberId, setDesktopMemberId] = useState<number | null>(null);
  const [actionLoading, setActionLoading] = useState<string | null>(null);
  const [selectedMemberInstance, setSelectedMemberInstance] =
    useState<Instance | null>(null);
  const [memberInstanceLoading, setMemberInstanceLoading] = useState(false);
  const [memberInstanceError, setMemberInstanceError] = useState<string | null>(
    null,
  );

  useEffect(() => {
    setLoadedTasks([]);
    setLoadedEvents([]);
    setHasMoreTasks(false);
    setHasMoreEvents(false);
    taskHistoryExhausted.current = false;
    eventHistoryExhausted.current = false;
    setHistoryError(null);
  }, [teamId]);

  const loadTeam = useCallback(
    async (options?: { background?: boolean }) => {
      if (!teamId || Number.isNaN(teamId)) {
        setError("Team ID 无效");
        setLoading(false);
        return;
      }
      try {
        if (options?.background) {
          setRefreshing(true);
        } else {
          setLoading(true);
        }
        const data = await teamService.getTeam(teamId);
        setDetails(data);
        setLoadedTasks((current) => mergeByIdDesc(data.tasks || [], current));
        setLoadedEvents((current) => mergeByIdDesc(data.events || [], current));
        setHasMoreTasks((current) =>
          taskHistoryExhausted.current
            ? false
            : current || (data.tasks?.length || 0) >= TEAM_TASK_HISTORY_PAGE_SIZE,
        );
        setHasMoreEvents((current) =>
          eventHistoryExhausted.current
            ? false
            : current || (data.events?.length || 0) >= TEAM_EVENT_HISTORY_PAGE_SIZE,
        );
        setError(null);
        setTargetMember((current) => current || "");
        setDesktopMemberId((current) =>
          current && data.members.some((member) => member.id === current)
            ? current
            : data.leader?.id || data.members[0]?.id || null,
        );
      } catch (err: any) {
        setError(err.response?.data?.error || "加载 Team 失败");
      } finally {
        setLoading(false);
        setRefreshing(false);
      }
    },
    [teamId],
  );

  useEffect(() => {
    void loadTeam();
  }, [loadTeam]);

  useEffect(() => {
    const timer = window.setInterval(() => {
      void loadTeam({ background: true });
    }, 5000);
    return () => window.clearInterval(timer);
  }, [loadTeam]);

  const memberById = useMemo(() => {
    const result = new Map<number, TeamMember>();
    details?.members.forEach((member) => result.set(member.id, member));
    return result;
  }, [details?.members]);

  const leader = details?.leader || details?.members.find((member) => member.role === "leader");
  const selectedDesktopMember =
    details?.members.find((member) => member.id === desktopMemberId) || leader;
  const tasks = loadedTasks.length > 0 ? loadedTasks : details?.tasks || [];
  const events = loadedEvents.length > 0 ? loadedEvents : details?.events || [];
  const selectedMemberInstanceResolved =
    selectedMemberInstance?.id === selectedDesktopMember?.instance_id;
  const selectedAccessRuntimeType = selectedMemberInstanceResolved && selectedMemberInstance
    ? selectedMemberInstance.runtime_type
    : null;
  const currentUserLabel = useMemo(() => {
    const username = typeof user?.username === "string" ? user.username.trim() : "";
    const email = typeof user?.email === "string" ? user.email.trim() : "";
    const baseLabel = username || email;
    return baseLabel ? `${baseLabel}（当前用户）` : "当前用户";
  }, [user?.email, user?.username]);
  const currentUserKey =
    typeof user?.id === "number" ? `user-${user.id}` : "current-user";

  useEffect(() => {
    const instanceId = selectedDesktopMember?.instance_id;
    if (!instanceId) {
      setSelectedMemberInstance(null);
      setMemberInstanceError(null);
      setMemberInstanceLoading(false);
      return;
    }

    let disposed = false;
    setMemberInstanceLoading(true);
    setMemberInstanceError(null);

    void instanceService
      .getInstance(instanceId)
      .then((instance) => {
        if (!disposed) {
          setSelectedMemberInstance(instance);
        }
      })
      .catch((err: any) => {
        if (!disposed) {
          setSelectedMemberInstance(null);
          setMemberInstanceError(
            err.response?.data?.error || "加载成员实例访问方式失败",
          );
        }
      })
      .finally(() => {
        if (!disposed) {
          setMemberInstanceLoading(false);
        }
      });

    return () => {
      disposed = true;
    };
  }, [selectedDesktopMember?.instance_id]);

  const handleDeleteTeam = async () => {
    if (!teamId || !window.confirm(`删除 Team「${details?.team.name || teamId}」？`)) {
      return;
    }
    try {
      setActionLoading("delete-team");
      await teamService.deleteTeam(teamId);
      navigate("/teams");
    } catch (err: any) {
      alert(err.response?.data?.error || "删除 Team 失败");
    } finally {
      setActionLoading(null);
    }
  };

  const handleDeleteMember = async (member: TeamMember) => {
    if (!teamId || !window.confirm(`删除成员「${member.member_key}」？`)) {
      return;
    }
    try {
      setActionLoading(`delete-member-${member.id}`);
      await teamService.deleteMember(teamId, member.id);
      await loadTeam({ background: true });
    } catch (err: any) {
      alert(err.response?.data?.error || "删除成员失败");
    } finally {
      setActionLoading(null);
    }
  };

  const handleDispatch = async (event: React.FormEvent) => {
    event.preventDefault();
    if (!teamId || !taskPrompt.trim()) {
      setDispatchError("任务内容不能为空");
      return;
    }
    try {
      setDispatching(true);
      setDispatchError(null);
      await teamService.dispatchTask(teamId, {
        target_member_id: targetMember.trim(),
        payload: {
          title: taskTitle.trim() || "Team task",
          prompt: taskPrompt.trim(),
        },
      });
      setTaskPrompt("");
      await loadTeam({ background: true });
    } catch (err: any) {
      setDispatchError(err.response?.data?.error || "派发任务失败");
    } finally {
      setDispatching(false);
    }
  };

  const handleLoadMoreHistory = async () => {
    if (!teamId || historyLoading || (!hasMoreTasks && !hasMoreEvents)) {
      return;
    }
    try {
      setHistoryLoading(true);
      setHistoryError(null);
      const [taskHistory, eventHistory] = await Promise.all([
        hasMoreTasks
          ? teamService.getTeamTasks(teamId, oldestID(tasks), TEAM_TASK_HISTORY_PAGE_SIZE)
          : Promise.resolve(null),
        hasMoreEvents
          ? teamService.getTeamEvents(teamId, oldestID(events), TEAM_EVENT_HISTORY_PAGE_SIZE)
          : Promise.resolve(null),
      ]);
      if (taskHistory) {
        setLoadedTasks((current) => mergeByIdDesc(current, taskHistory.tasks || []));
        setHasMoreTasks(taskHistory.has_more);
        taskHistoryExhausted.current = !taskHistory.has_more;
      }
      if (eventHistory) {
        setLoadedEvents((current) => mergeByIdDesc(current, eventHistory.events || []));
        setHasMoreEvents(eventHistory.has_more);
        eventHistoryExhausted.current = !eventHistory.has_more;
      }
    } catch (err: any) {
      setHistoryError(err.response?.data?.error || "加载历史消息失败");
    } finally {
      setHistoryLoading(false);
    }
  };

  if (loading) {
    return (
      <UserLayout>
        <div className="flex min-h-[60vh] items-center justify-center text-lg text-gray-600">
          正在加载...
        </div>
      </UserLayout>
    );
  }

  if (error || !details) {
    return (
      <UserLayout title="Team">
        <div className="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-red-700">
          {error || "Team 不存在"}
        </div>
      </UserLayout>
    );
  }

  return (
    <UserLayout title={details.team.name}>
      <div className="space-y-6">
        <div className="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
          <div>
            <div className="flex flex-wrap items-center gap-3">
              <span
                className={`inline-flex items-center rounded-full border px-2.5 py-0.5 text-xs font-medium ${statusStyle(details.team.status)}`}
              >
                {details.team.status}
              </span>
              <span className="text-sm text-gray-500">
                Team #{details.team.id}
              </span>
              {refreshing && (
                <span className="text-sm text-gray-400">刷新中...</span>
              )}
            </div>
            <p className="mt-2 text-sm text-gray-600">
              Leader：{details.leader_member_id || "-"} · 共享目录：
              {details.team.shared_mount_path}
            </p>
          </div>
          <div className="flex flex-wrap gap-3">
            <button
              type="button"
              onClick={() => void loadTeam({ background: true })}
              className="inline-flex items-center justify-center rounded-xl border border-[#eadfd8] bg-white px-4 py-2 text-sm font-medium text-[#5f5957] hover:bg-[#fff8f5]"
            >
              刷新
            </button>
            <button
              type="button"
              onClick={handleDeleteTeam}
              disabled={actionLoading === "delete-team"}
              className="inline-flex items-center justify-center rounded-xl border border-red-200 bg-red-50 px-4 py-2 text-sm font-medium text-red-700 hover:bg-red-100 disabled:cursor-not-allowed disabled:opacity-50"
            >
              {actionLoading === "delete-team" ? "删除中..." : "删除 Team"}
            </button>
            <Link to="/teams" className="app-button-secondary">
              返回列表
            </Link>
          </div>
        </div>

        <section className="app-panel p-4">
          <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
            <h2 className="text-lg font-semibold text-gray-900">成员桌面</h2>
            <select
              value={desktopMemberId ?? ""}
              onChange={(event) => setDesktopMemberId(Number(event.target.value))}
              className="rounded-xl border border-[#eadfd8] px-3 py-2 text-sm focus:border-[#ef4444] focus:outline-none focus:ring-1 focus:ring-[#f3d2c2]"
            >
              {details.members.map((member) => (
                <option key={member.id} value={member.id}>
                  {member.member_key} · {member.role}
                </option>
              ))}
            </select>
          </div>
        </section>

        <div className="grid grid-cols-1 items-stretch gap-6 xl:h-[clamp(620px,calc((100vw-360px)*0.45),860px)] xl:grid-cols-[minmax(0,2fr)_minmax(360px,1fr)]">
          {selectedDesktopMember?.instance_id ? (
            <section className="flex h-full min-w-0 flex-col gap-3">
              <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
                <h2 className="text-lg font-semibold text-gray-900">
                  {selectedDesktopMember.role === "leader" ? "Leader" : selectedDesktopMember.member_key} 桌面
                </h2>
                <Link
                  to={`/instances/${selectedDesktopMember.instance_id}`}
                  className="inline-flex items-center justify-center rounded-xl border border-[#eadfd8] bg-white px-4 py-2 text-sm font-medium text-[#5f5957] hover:bg-[#fff8f5]"
                >
                  实例详情
                </Link>
              </div>
              {!selectedAccessRuntimeType && !memberInstanceError ? (
                <div className="app-panel flex min-h-[420px] flex-1 items-center justify-center border-dashed p-8 text-sm text-gray-500">
                  {memberInstanceLoading ? "正在加载成员访问方式..." : "正在准备成员访问方式..."}
                </div>
              ) : memberInstanceError ? (
                <div className="app-panel flex min-h-[420px] flex-1 items-center justify-center border-dashed p-8 text-center text-sm text-red-600">
                  {memberInstanceError}
                </div>
              ) : (
                <InstanceAccess
                  key={`${selectedDesktopMember.instance_id}-${selectedAccessRuntimeType}`}
                  instanceId={selectedDesktopMember.instance_id}
                  instanceName={selectedDesktopMember.display_name}
                  runtimeType={selectedAccessRuntimeType || "desktop"}
                  containerClassName="min-h-0 xl:flex-1 flex flex-col"
                  frameHeightClassName="h-[54vh] min-h-[420px] max-h-[720px] xl:h-auto xl:min-h-0 xl:max-h-none xl:flex-1"
                  isRunning={
                    selectedDesktopMember.status !== "creating" &&
                    selectedDesktopMember.status !== "failed" &&
                    selectedDesktopMember.status !== "offline" &&
                    selectedDesktopMember.status !== "deleting" &&
                    selectedDesktopMember.status !== "deleted"
                  }
                />
              )}
            </section>
          ) : (
            <div className="app-panel border-dashed p-8 text-center text-sm text-gray-500">
              所选成员实例还没有就绪。
            </div>
          )}

          <CollaborationPanel
            team={details.team}
            events={events}
            tasks={tasks}
            members={details.members}
            memberById={memberById}
            leaderMemberId={details.leader_member_id}
            currentUserLabel={currentUserLabel}
            currentUserKey={currentUserKey}
            taskPrompt={taskPrompt}
            dispatching={dispatching}
            dispatchError={dispatchError}
            historyLoading={historyLoading}
            historyError={historyError}
            hasMoreHistory={hasMoreTasks || hasMoreEvents}
            onTaskPromptChange={setTaskPrompt}
            onDispatch={handleDispatch}
            onLoadMoreHistory={handleLoadMoreHistory}
          />
        </div>

        <div className="grid grid-cols-1 gap-6 xl:grid-cols-[minmax(0,2fr)_minmax(360px,1fr)]">
          <section className="app-panel overflow-hidden">
            <div className="border-b border-[#f1e7e1] px-5 py-4">
              <h2 className="text-lg font-semibold text-gray-900">成员</h2>
            </div>
            <div className="overflow-x-auto">
              <table className="min-w-full divide-y divide-[#f1e7e1] text-sm">
                <thead className="bg-[#fff8f5] text-left text-xs font-semibold uppercase tracking-[0.14em] text-[#b46c50]">
                  <tr>
                    <th className="px-5 py-3">成员</th>
                    <th className="px-5 py-3">角色</th>
                    <th className="px-5 py-3">Runtime</th>
                    <th className="px-5 py-3">职责</th>
                    <th className="px-5 py-3">状态</th>
                    <th
                      className="px-5 py-3"
                      title="Runtime 最近上报的可用态，和 ClawManager 调度状态分开显示"
                    >
                      Runtime 可用态
                    </th>
                    <th className="px-5 py-3">最后在线</th>
                    <th className="px-5 py-3">实例</th>
                    <th className="px-5 py-3">操作</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-[#f1e7e1] bg-white">
                  {details.members.map((member) => (
                    <tr key={member.id}>
                      <td className="px-5 py-4">
                        <div className="font-medium text-gray-900">
                          {member.display_name}
                        </div>
                        <div className="mt-1 font-mono text-xs text-gray-500">
                          {member.member_key}
                        </div>
                      </td>
                      <td className="px-5 py-4 text-gray-600">{member.role}</td>
                      <td className="px-5 py-4 text-gray-600">
                        {member.runtime_type || "openclaw"}
                      </td>
                      <td className="min-w-[280px] max-w-md px-5 py-4">
                        <DescriptionPreview text={member.description} />
                      </td>
                      <td className="px-5 py-4">
                        <span
                          className={`inline-flex items-center rounded-full border px-2.5 py-0.5 text-xs font-medium ${statusStyle(member.status)}`}
                        >
                          {member.status}
                        </span>
                      </td>
                      <td className="max-w-xs px-5 py-4">
                        <span
                          className={`inline-flex items-center rounded-full border px-2.5 py-0.5 text-xs font-medium ${availabilityStyle(member.availability)}`}
                        >
                          {member.availability || "unknown"}
                        </span>
                        {(member.blocked_reason || member.last_summary) && (
                          <div className="mt-2 line-clamp-3 text-xs text-gray-500">
                            {member.blocked_reason || member.last_summary}
                          </div>
                        )}
                        {(member.runtime_task_id || member.runtime_intent) && (
                          <div className="mt-1 break-all font-mono text-[11px] text-gray-400">
                            {member.runtime_intent || "-"} ·{" "}
                            {member.runtime_task_id || "-"}
                          </div>
                        )}
                      </td>
                      <td className="px-5 py-4 text-gray-600">
                        {formatDateTime(member.last_seen_at)}
                      </td>
                      <td className="px-5 py-4">
                        {member.instance_id ? (
                          <Link
                            to={`/instances/${member.instance_id}`}
                            className="text-[#dc2626] hover:underline"
                          >
                            #{member.instance_id}
                          </Link>
                        ) : (
                          "-"
                        )}
                      </td>
                      <td className="px-5 py-4">
                        <div className="flex flex-wrap gap-2">
                          <button
                            type="button"
                            onClick={() => setDesktopMemberId(member.id)}
                            className="rounded-lg border border-[#eadfd8] bg-white px-3 py-1.5 text-xs font-medium text-[#5f5957] hover:bg-[#fff8f5]"
                          >
                            桌面
                          </button>
                          <button
                            type="button"
                            disabled={
                              member.role === "leader" ||
                              actionLoading === `delete-member-${member.id}`
                            }
                            onClick={() => void handleDeleteMember(member)}
                            className="rounded-lg border border-red-200 bg-red-50 px-3 py-1.5 text-xs font-medium text-red-700 hover:bg-red-100 disabled:cursor-not-allowed disabled:opacity-50"
                          >
                            {actionLoading === `delete-member-${member.id}`
                              ? "删除中..."
                              : "删除"}
                          </button>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </section>

          <aside className="space-y-6">
            <section className="app-panel p-5">
              <h2 className="text-lg font-semibold text-gray-900">调试派发</h2>
              <p className="mt-1 text-sm text-gray-500">
                留空目标会按后端规则投给 Leader；直接选择成员仅用于 smoke、调试或外部集成。
              </p>
              <form onSubmit={handleDispatch} className="mt-4 space-y-4">
                <label className="block">
                  <span className="text-sm font-medium text-gray-700">
                    目标成员
                  </span>
                  <select
                    value={targetMember}
                    onChange={(event) => setTargetMember(event.target.value)}
                    className="mt-1 block w-full rounded-xl border border-[#eadfd8] px-3 py-2 text-sm focus:border-[#ef4444] focus:outline-none focus:ring-1 focus:ring-[#f3d2c2]"
                  >
                    <option value="">
                      默认 Leader（{details.leader_member_id || "-"}）
                    </option>
                    {details.members.map((member) => (
                      <option key={member.id} value={member.member_key}>
                        {member.member_key} · {member.role}
                      </option>
                    ))}
                  </select>
                </label>
                <label className="block">
                  <span className="text-sm font-medium text-gray-700">标题</span>
                  <input
                    value={taskTitle}
                    onChange={(event) => setTaskTitle(event.target.value)}
                    className="mt-1 block w-full rounded-xl border border-[#eadfd8] px-3 py-2 text-sm focus:border-[#ef4444] focus:outline-none focus:ring-1 focus:ring-[#f3d2c2]"
                  />
                </label>
                <label className="block">
                  <span className="text-sm font-medium text-gray-700">内容</span>
                  <textarea
                    value={taskPrompt}
                    onChange={(event) => setTaskPrompt(event.target.value)}
                    rows={5}
                    className="mt-1 block w-full rounded-xl border border-[#eadfd8] px-3 py-2 text-sm focus:border-[#ef4444] focus:outline-none focus:ring-1 focus:ring-[#f3d2c2]"
                  />
                </label>
                {dispatchError && (
                  <p className="rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700">
                    {dispatchError}
                  </p>
                )}
                <button
                  type="submit"
                  disabled={dispatching}
                  className="app-button-primary w-full disabled:cursor-not-allowed disabled:opacity-50"
                >
                  {dispatching ? "派发中..." : "派发"}
                </button>
              </form>
            </section>

            <MetaPanel details={details} />
          </aside>
        </div>

      </div>
    </UserLayout>
  );
};

function MetaPanel({ details }: { details: TeamDetails }) {
  return (
    <section className="app-panel p-5">
      <h2 className="text-lg font-semibold text-gray-900">运行信息</h2>
      <dl className="mt-4 space-y-3 text-sm">
        <MetaRow label="通信模式" value={details.team.communication_mode} />
        <MetaRow label="共享 PVC" value={details.team.shared_pvc_name || "-"} />
        <MetaRow
          label="命名空间"
          value={details.team.shared_pvc_namespace || "-"}
        />
        <MetaRow label="StorageClass" value={details.team.storage_class || "-"} />
        <MetaRow label="Events ID" value={details.team.redis_events_last_id} />
      </dl>
    </section>
  );
}

function DescriptionPreview({ text }: { text?: string }) {
  const [expanded, setExpanded] = useState(false);
  const normalized = (text || "").trim();
  if (!normalized) {
    return <span className="text-sm text-gray-400">-</span>;
  }

  const lines = normalized.split(/\r?\n/);
  const previewLines = lines.slice(0, 5);
  const previewText = previewLines.join("\n");
  const hasMore = lines.length > 5 || normalized.length > 280;

  return (
    <div className="group rounded-xl border border-[#f1e7e1] bg-[#fffaf7] px-3 py-2.5 text-sm leading-6 text-gray-700 shadow-[0_10px_22px_-22px_rgba(72,44,24,0.45)]">
      <div className={expanded ? "" : "max-h-[7.5rem] overflow-hidden"}>
        <MarkdownContent text={expanded || !hasMore ? normalized : previewText} compact />
      </div>
      {hasMore && (
        <button
          type="button"
          onClick={() => setExpanded((current) => !current)}
          className="mt-2 inline-flex items-center rounded-full border border-[#eadfd8] bg-white px-2.5 py-1 text-xs font-medium text-[#8b5a45] transition hover:border-[#ef6b4a] hover:text-[#dc2626]"
        >
          {expanded ? "收起" : `展开 ${Math.max(lines.length - previewLines.length, 1)} 行`}
        </button>
      )}
    </div>
  );
}

function CollaborationPanel({
  team,
  events,
  tasks,
  members,
  memberById,
  leaderMemberId,
  currentUserLabel,
  currentUserKey,
  taskPrompt,
  dispatching,
  dispatchError,
  historyLoading,
  historyError,
  hasMoreHistory,
  onTaskPromptChange,
  onDispatch,
  onLoadMoreHistory,
}: {
  team: TeamDetails["team"];
  events: TeamEvent[];
  tasks: TeamTask[];
  members: TeamMember[];
  memberById: Map<number, TeamMember>;
  leaderMemberId?: string;
  currentUserLabel: string;
  currentUserKey: string;
  taskPrompt: string;
  dispatching: boolean;
  dispatchError: string | null;
  historyLoading: boolean;
  historyError: string | null;
  hasMoreHistory: boolean;
  onTaskPromptChange: (value: string) => void;
  onDispatch: (event: React.FormEvent) => void;
  onLoadMoreHistory: () => void;
}) {
  const groups = buildCollaborationGroups(events, tasks, memberById);
  const messages = buildTeamChatMessages(
    groups,
    memberById,
    leaderMemberId,
    currentUserLabel,
    currentUserKey,
  );
  const onlineCount = members.filter(
    (member) => !["offline", "deleted", "deleting"].includes(member.status),
  ).length;

  return (
    <section className="app-panel flex h-full min-h-0 flex-col overflow-hidden rounded-[22px]">
      <div className="shrink-0 border-b border-[#e8e8e8] bg-white px-4 py-3">
        <div className="flex items-start">
          <div className="min-w-0 flex-1">
            <h2 className="text-base font-semibold leading-6 text-gray-950">团队群聊</h2>
            <div className="mt-0.5 truncate text-xs text-gray-500">
              Team #{team.id} · {team.status}
            </div>
            <div className="mt-1 flex items-center gap-2 text-xs text-gray-500">
              <span className="h-2 w-2 rounded-full bg-emerald-400" />
              <span>{onlineCount}人在线</span>
            </div>
          </div>
        </div>
      </div>

      <div
        className="min-h-0 flex-1 overflow-auto bg-[#f5f5f5]"
        onScroll={(event) => {
          if (event.currentTarget.scrollTop <= 24 && hasMoreHistory && !historyLoading) {
            void onLoadMoreHistory();
          }
        }}
      >
        {messages.length === 0 ? (
          <div className="p-6 text-center text-xs text-gray-500">暂无群聊消息。</div>
        ) : (
          <div className="space-y-5 px-4 py-5">
            {(hasMoreHistory || historyLoading || historyError) && (
              <div className="space-y-2 text-center">
                {hasMoreHistory && (
                  <button
                    type="button"
                    disabled={historyLoading}
                    onClick={() => void onLoadMoreHistory()}
                    className="inline-flex items-center gap-2 rounded-full border border-[#dddddd] bg-white px-4 py-2 text-xs font-medium text-gray-500 shadow-sm transition hover:border-gray-300 hover:text-gray-700 disabled:cursor-wait disabled:opacity-70"
                  >
                    <span className="text-base leading-none">↑</span>
                    <span>{historyLoading ? "加载历史消息中..." : "向上滑动或点击查看更多历史消息"}</span>
                  </button>
                )}
                {!hasMoreHistory && historyLoading && (
                  <span className="inline-flex rounded-full border border-[#dddddd] bg-white px-4 py-2 text-xs text-gray-500 shadow-sm">
                    加载历史消息中...
                  </span>
                )}
                {historyError && (
                  <div className="text-xs text-red-600">{historyError}</div>
                )}
              </div>
            )}
            <TimeDivider value={messages[0]?.time} />
            {messages.map((message) =>
              message.kind === "system" ? (
                <SystemChatLine key={message.id} message={message} />
              ) : (
                <TeamChatMessageRow key={message.id} message={message} />
              ),
            )}
          </div>
        )}
      </div>

      <div className="shrink-0 border-t border-[#dddddd] bg-white px-4 py-3">
        {dispatchError && (
          <div className="mb-2 rounded-lg border border-red-100 bg-red-50 px-3 py-2 text-xs text-red-700">
            {dispatchError}
          </div>
        )}
        <form onSubmit={onDispatch} className="flex items-end gap-2">
          <textarea
            value={taskPrompt}
            onChange={(event) => onTaskPromptChange(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter" && !event.shiftKey) {
                event.preventDefault();
                event.currentTarget.form?.requestSubmit();
              }
            }}
            rows={1}
            placeholder="发送消息..."
            className="max-h-24 min-h-[38px] flex-1 resize-none rounded-full border border-[#d9d9d9] bg-white px-4 py-2 text-xs leading-5 text-gray-900 outline-none transition focus:border-[#9ca3af] focus:ring-2 focus:ring-gray-100"
          />
          <button
            type="submit"
            disabled={dispatching || !taskPrompt.trim()}
            className="flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-[#1f2937] text-white transition hover:bg-[#111827] disabled:cursor-not-allowed disabled:bg-gray-300"
            aria-label="发送任务"
            title="发送任务"
          >
            {dispatching ? (
              <span className="h-4 w-4 animate-spin rounded-full border-2 border-white/30 border-t-white" />
            ) : (
              <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M5 12h14m-6-6 6 6-6 6" />
              </svg>
            )}
          </button>
        </form>
      </div>
    </section>
  );
}

type TeamChatMessage = {
  id: string;
  kind: "member" | "system";
  sender: string;
  senderKey: string;
  content: string;
  time: number;
  tone?: "normal" | "leader" | "assignment" | "feedback" | "error";
  dedupeKey?: string;
  threadKey?: string;
  sortPhase?: number;
};

function buildTeamChatMessages(
  groups: CollaborationGroup[],
  memberById: Map<number, TeamMember>,
  leaderMemberId?: string,
  currentUserLabel = "当前用户",
  currentUserKey = "current-user",
) {
  const messages: TeamChatMessage[] = [];
  const memberByKey = new Map(
    [...memberById.values()].map((member) => [member.member_key, member]),
  );
  for (const group of groups) {
    if (group.task) {
      const target =
        memberById.get(group.task.target_member_id)?.member_key ||
        `#${group.task.target_member_id}`;
      const targetLabel = displayMemberName(target, memberByKey, leaderMemberId);
      const creatorKey = taskCreatorKey(group.task);
      const prompt = taskPromptText(group.task) || group.title;
      const assignmentSenderKey =
        creatorKey === currentUserKey || creatorKey === "user"
          ? currentUserKey
          : creatorKey;
      const assignmentSender =
        assignmentSenderKey === currentUserKey
          ? currentUserLabel
          : displayMemberName(creatorKey, memberByKey, leaderMemberId);
      messages.push({
        id: `task-${group.task.id}`,
        kind: "member",
        sender: assignmentSender,
        senderKey: assignmentSenderKey,
        content: `@${targetLabel} ${prompt}\n任务：${group.task.message_id || group.label}`,
        time: new Date(group.task.created_at).getTime(),
        tone: "assignment",
        dedupeKey: `assignment:${group.task.message_id || group.task.id}`,
        threadKey: group.key,
        sortPhase: 0,
      });
      const resultSummary =
        payloadText(group.task.result, ["summary", "result", "message", "text"]) ||
        payloadText(group.task.payload, ["result", "answer"]);
      if (resultSummary && group.items.length === 0) {
        messages.push({
          id: `task-result-${group.task.id}`,
          kind: "member",
          sender: targetLabel,
          senderKey: target,
          content: `任务结果反馈：\n${resultSummary}`,
          time: new Date(group.task.finished_at || group.task.updated_at).getTime(),
          tone: "feedback",
          dedupeKey: `feedback:${group.task.message_id || group.task.id}:${normalizeChatDedupeContent(resultSummary)}`,
          threadKey: group.key,
          sortPhase: 2,
        });
      }
      if (group.task.error_message && group.items.length === 0) {
        messages.push({
          id: `task-error-${group.task.id}`,
          kind: "member",
          sender: targetLabel,
          senderKey: target,
          content: `失败：${group.task.error_message}`,
          time: new Date(group.task.updated_at).getTime(),
          tone: "error",
          dedupeKey: `error:${group.task.message_id || group.task.id}:${normalizeChatDedupeContent(group.task.error_message)}`,
          threadKey: group.key,
          sortPhase: 2,
        });
      }
    }

    for (const item of group.items) {
      if (isTaskDispatchEcho(item, group.task) || isProtocolProgressEcho(item)) {
        continue;
      }
      const message = chatMessageFromItem(item, memberByKey, leaderMemberId);
      if (message) {
        messages.push(message);
      }
    }
  }
  return messages
    .filter((message) => Number.isFinite(message.time))
    .sort(compareTeamChatMessages)
    .filter(dedupeTeamChatMessage());
}

function compareTeamChatMessages(a: TeamChatMessage, b: TeamChatMessage) {
  if (a.threadKey && a.threadKey === b.threadKey) {
    const phaseDiff = (a.sortPhase ?? 1) - (b.sortPhase ?? 1);
    if (phaseDiff !== 0) {
      return phaseDiff;
    }
  }
  return a.time - b.time || a.id.localeCompare(b.id);
}

function itemMessageID(item: CollaborationItem) {
  return payloadText(item.payload, ["messageId", "message_id"]) || item.event.message_id || "";
}

function isTaskDispatchEcho(item: CollaborationItem, task?: TeamTask) {
  if (!task) {
    return false;
  }
  if (item.eventType !== "outbound" && item.eventType !== "task_assigned") {
    return false;
  }
  return item.event.task_id === task.id || itemMessageID(item) === task.message_id;
}

function isProtocolProgressEcho(item: CollaborationItem) {
  return ["task_received", "task_started", "progress", "task_progress"].includes(
    item.eventType,
  );
}

function chatMessageFromItem(
  item: CollaborationItem,
  memberByKey: Map<string, TeamMember>,
  leaderMemberId?: string,
): TeamChatMessage | null {
  const status = payloadText(item.payload, ["status"]);
  const progress = payloadNumber(item.payload, ["progress"]);
  const senderKey = item.actor || item.from || "system";
  const senderLabel = displayMemberName(senderKey, memberByKey, leaderMemberId);
  const targetLabel = item.to
    ? displayMemberName(item.to, memberByKey, leaderMemberId)
    : "";
  const isAssignmentEvent =
    item.eventType === "outbound" || item.eventType === "task_assigned";
  const hasContent = Boolean(item.content.trim());
  const isFeedbackEvent =
    isWorkerToLeaderMessage(senderKey, item.to, leaderMemberId) ||
    isWorkerFeedbackEvent(item, senderKey, leaderMemberId, hasContent);
  const isSystem = item.eventType === "task_stale" || (isAssignmentEvent && !hasContent);
  const fallbackContent =
    isAssignmentEvent && !hasContent
      ? assignmentEventFallback(item, senderLabel, targetLabel, isFeedbackEvent)
      : chatFallbackText(item, progress, status);
  const content = item.content || fallbackContent;
  const isTerminalFeedback = isTerminalFeedbackItem(item, content, isFeedbackEvent);
  return {
    id: `event-${item.event.id}`,
    kind: isSystem ? "system" : "member",
    sender: isSystem ? "系统" : senderLabel,
    senderKey,
    content,
    time: item.timeMs,
    tone:
      isAssignmentEvent && hasContent
        ? isFeedbackEvent
          ? "feedback"
          : "assignment"
        : isAssignmentEvent && isFeedbackEvent
          ? "feedback"
        : item.eventType === "task_failed" || item.eventType === "message_failed"
          ? "error"
          : senderKey === leaderMemberId || senderKey === "ClawManager"
            ? "leader"
            : "normal",
    dedupeKey: chatItemDedupeKey(item, senderKey, content, isAssignmentEvent, isFeedbackEvent),
    threadKey: item.taskKey,
    sortPhase: chatItemSortPhase(item, isAssignmentEvent, isTerminalFeedback),
  };
}

function chatItemSortPhase(
  item: CollaborationItem,
  isAssignmentEvent: boolean,
  isTerminalFeedback: boolean,
) {
  if (isAssignmentEvent && !isTerminalFeedback) {
    return 0;
  }
  if (
    isTerminalFeedback ||
    item.eventType === "task_stale" ||
    item.eventType === "task_failed" ||
    item.eventType === "message_failed"
  ) {
    return 2;
  }
  return 1;
}

function isTerminalFeedbackItem(
  item: CollaborationItem,
  content: string,
  isFeedbackEvent: boolean,
) {
  if (
    item.eventType === "task_completed" ||
    item.eventType === "completion" ||
    item.eventType === "task_failed" ||
    item.eventType === "message_failed"
  ) {
    return true;
  }
  return isFeedbackEvent && finalFeedbackContentPattern.test(content);
}

const finalFeedbackContentPattern =
  /\bDONE\b|team_complete_task|任务核心结果|完整详细产出|结果已反馈|已完成|执行完成|完成任务/;

function dedupeTeamChatMessage() {
  const seen = new Set<string>();
  return (message: TeamChatMessage) => {
    if (!message.dedupeKey) {
      return true;
    }
    if (seen.has(message.dedupeKey)) {
      return false;
    }
    seen.add(message.dedupeKey);
    return true;
  };
}

function chatItemDedupeKey(
  item: CollaborationItem,
  senderKey: string,
  content: string,
  isAssignmentEvent: boolean,
  isFeedbackEvent: boolean,
) {
  const messageId =
    payloadTextDeep(item.payload, ["messageId", "message_id", "inReplyTo", "in_reply_to"]) ||
    item.event.message_id ||
    "";
  const taskId =
    payloadTextDeep(item.payload, ["taskId", "task_id", "runtimeTaskId"]) ||
    (item.event.task_id ? canonicalTaskKey(item.event.task_id) : item.taskKey);
  const contentKey = normalizeChatDedupeContent(content);
  if (isAssignmentEvent) {
    return `assignment:${messageId || taskId}:${senderKey}:${item.to || ""}:${contentKey}`;
  }
  if (isFeedbackEvent) {
    return `feedback:${messageId || taskId}:${senderKey}:${item.to || ""}:${contentKey}`;
  }
  if (item.eventType === "task_completed" || item.eventType === "completion" || item.eventType === "reply") {
    return `feedback:${messageId || taskId}:${senderKey}:${item.to || ""}:${contentKey}`;
  }
  return "";
}

function normalizeChatDedupeContent(content: string) {
  return content.trim().replace(/\s+/g, " ").slice(0, 240);
}

function assignmentEventFallback(
  item: CollaborationItem,
  senderLabel: string,
  targetLabel: string,
  isFeedbackEvent = false,
) {
  const title = isFeedbackEvent ? "任务结果反馈事件" : "任务派发事件";
  const parts = [`${title}：${senderLabel}${targetLabel ? ` → ${targetLabel}` : ""}`];
  const taskId =
    payloadTextDeep(item.payload, ["taskId", "task_id", "runtimeTaskId"]) ||
    item.taskLabel;
  const messageId =
    payloadTextDeep(item.payload, ["messageId", "message_id"]) ||
    item.event.message_id ||
    "";
  if (taskId) {
    parts.push(`任务：${taskId}`);
  }
  if (messageId) {
    parts.push(`消息：${messageId}`);
  }
  parts.push("该事件未携带正文，无法展示任务内容。");
  return parts.join("\n");
}

function isWorkerToLeaderMessage(
  senderKey: string,
  targetKey?: string,
  leaderMemberId?: string,
) {
  if (!targetKey) {
    return false;
  }
  const normalizedLeader = leaderMemberId || "leader";
  const targetIsLeader = targetKey === normalizedLeader || targetKey === "leader";
  const senderIsLeader =
    senderKey === normalizedLeader ||
    senderKey === "leader" ||
    senderKey === "ClawManager";
  return targetIsLeader && !senderIsLeader;
}

function isLeaderMemberKey(memberKey: string, leaderMemberId?: string) {
  const normalizedLeader = leaderMemberId || "leader";
  return (
    memberKey === normalizedLeader ||
    memberKey === "leader" ||
    memberKey === "ClawManager"
  );
}

function isWorkerFeedbackEvent(
  item: CollaborationItem,
  senderKey: string,
  leaderMemberId: string | undefined,
  hasContent: boolean,
) {
  if (isLeaderMemberKey(senderKey, leaderMemberId)) {
    return false;
  }
  if (
    item.eventType === "reply" ||
    item.eventType === "completion" ||
    item.eventType === "task_completed"
  ) {
    return true;
  }
  if (!hasContent) {
    return false;
  }
  return /\bDONE\b|@Leader|team_complete_task|任务核心结果|结果|完成/.test(item.content);
}

function displayMemberName(
  memberKey: string,
  memberByKey: Map<string, TeamMember>,
  leaderMemberId?: string,
) {
  const member = memberByKey.get(memberKey);
  if (member) {
    return `${member.display_name || member.member_key}（${member.member_key}）`;
  }
  if (memberKey === leaderMemberId) {
    return `Leader（${memberKey}）`;
  }
  if (memberKey === "ClawManager") {
    return "ClawManager（system）";
  }
  if (memberKey === "user") {
    return "User";
  }
  if (memberKey.startsWith("user-")) {
    return `User #${memberKey.slice("user-".length)}`;
  }
  return memberKey;
}

function TimeDivider({ value }: { value?: number }) {
  if (!value) {
    return null;
  }
  return (
    <div className="flex items-center justify-center gap-3 text-xs text-gray-500">
      <span className="h-px w-8 bg-gray-300" />
      <span>{formatChatTime(value)}</span>
      <span className="h-px w-8 bg-gray-300" />
    </div>
  );
}

function TeamChatMessageRow({ message }: { message: TeamChatMessage }) {
  const bubbleClass =
    message.tone === "assignment"
      ? "relative overflow-hidden border border-amber-200 bg-gradient-to-br from-amber-50 via-white to-orange-50 text-gray-950 shadow-[0_14px_28px_-22px_rgba(180,83,9,0.8)]"
      : message.tone === "feedback"
      ? "relative overflow-hidden border border-emerald-200 bg-gradient-to-br from-emerald-50 via-white to-green-50 text-gray-950 shadow-[0_14px_28px_-22px_rgba(5,150,105,0.55)]"
      : message.tone === "error"
      ? "border border-red-100 bg-red-50 text-red-800"
      : "bg-white text-gray-950";
  const isAssignment = message.tone === "assignment";
  const isFeedback = message.tone === "feedback";
  const markerClass = isFeedback
    ? "border-emerald-100 text-emerald-700"
    : "border-amber-100 text-amber-700";
  const markerDotClass = isFeedback ? "bg-emerald-400" : "bg-amber-400";
  const markerDotSolidClass = isFeedback ? "bg-emerald-500" : "bg-amber-500";
  return (
    <div className="flex items-start gap-3">
      <MemberAvatar name={message.senderKey} />
      <div className="min-w-0 flex-1">
        <div className="mb-1 flex items-center gap-2">
          <span className="truncate text-xs font-medium text-gray-500">{message.sender}</span>
          <span className="shrink-0 text-xs text-gray-400">{formatChatTime(message.time)}</span>
        </div>
        <div className={`inline-block max-w-[92%] rounded-lg px-3.5 py-2.5 text-sm leading-6 shadow-sm ${bubbleClass}`}>
          {(isAssignment || isFeedback) && (
            <div className={`mb-2 flex items-center gap-2 border-b pb-2 text-[11px] font-semibold uppercase tracking-[0.12em] ${markerClass}`}>
              <span className="relative flex h-2 w-2">
                <span className={`absolute inline-flex h-full w-full animate-ping rounded-full opacity-60 ${markerDotClass}`} />
                <span className={`relative inline-flex h-2 w-2 rounded-full ${markerDotSolidClass}`} />
              </span>
              <span>{isFeedback ? "任务结果反馈" : "任务下发"}</span>
            </div>
          )}
          <MarkdownContent text={message.content} compact />
        </div>
      </div>
    </div>
  );
}

function SystemChatLine({ message }: { message: TeamChatMessage }) {
  const systemClass =
    message.tone === "feedback"
      ? "bg-emerald-50 text-emerald-700 ring-1 ring-emerald-100"
      : "bg-gray-200 text-gray-500";
  const timeClass = message.tone === "feedback" ? "text-emerald-500/80" : "text-gray-400";
  return (
    <div className="flex justify-center">
      <div className={`max-w-[86%] rounded-2xl px-3 py-1.5 text-center text-[11px] leading-5 ${systemClass}`}>
        <div className={`mb-0.5 text-[10px] ${timeClass}`}>{formatChatTime(message.time)}</div>
        {message.content}
      </div>
    </div>
  );
}

function MemberAvatar({ name }: { name: string }) {
  const label = avatarLabel(name);
  const isLeader = name.toLowerCase().includes("leader") || name === "ClawManager";
  return (
    <div
      className={`flex h-10 w-10 shrink-0 items-center justify-center rounded-full border text-xs font-semibold shadow-sm ${
        isLeader
          ? "border-slate-300 bg-gradient-to-br from-slate-100 to-slate-300 text-slate-700"
          : "border-sky-200 bg-gradient-to-br from-sky-100 to-cyan-200 text-sky-800"
      }`}
    >
      {label}
    </div>
  );
}

function avatarLabel(name: string) {
  const normalized = name.replace(/^team-[^-]+-/, "").replace(/[^a-zA-Z0-9]/g, "");
  return (normalized || "AI").slice(0, 2).toUpperCase();
}

function formatChatTime(value: number) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "";
  }
  return date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

function chatFallbackText(
  item: CollaborationItem,
  progress?: number,
  status?: string,
) {
  switch (item.eventType) {
    case "outbound":
    case "task_assigned":
      return item.to ? `发布了给 ${item.to} 的任务分工。` : "发布了任务分工。";
    case "task_received":
      return "已领取任务。";
    case "task_started":
      return "开始执行任务。";
    case "progress":
    case "task_progress":
      return progress === undefined ? "进度已更新。" : `当前进度 ${progress}%。`;
    case "reply":
    case "completion":
    case "task_completed":
      return "结果已反馈。";
    case "task_failed":
    case "message_failed":
      return payloadText(item.payload, ["error", "error_message", "diagnostic"]) || "任务执行失败。";
    case "task_stale":
      return "长时间没有新的进展。";
    default:
      return status ? `状态更新：${status}` : eventVerb(item.eventType);
  }
}

function MarkdownContent({ text, compact = false }: { text: string; compact?: boolean }) {
  const lines = text.split(/\r?\n/);
  const nodes: React.ReactNode[] = [];

  for (let index = 0; index < lines.length; index++) {
    if (isMarkdownTableStart(lines, index)) {
      const tableLines = [lines[index]];
      const separator = lines[index + 1];
      let rowIndex = index + 2;
      while (rowIndex < lines.length && splitMarkdownTableRow(lines[rowIndex]).length > 1) {
        tableLines.push(lines[rowIndex]);
        rowIndex++;
      }
      nodes.push(
        <MarkdownTable
          key={`table-${index}`}
          headerLine={tableLines[0]}
          separatorLine={separator}
          rowLines={tableLines.slice(1)}
          keyPrefix={`table-${index}`}
        />,
      );
      index = rowIndex - 1;
      continue;
    }

    nodes.push(renderMarkdownLine(lines[index], index, compact));
  }

  return <div className={compact ? "space-y-1.5" : "space-y-2"}>{nodes}</div>;
}

function renderMarkdownLine(line: string, index: number, compact: boolean) {
  const trimmed = line.trim();
  if (!trimmed) {
    return <div key={index} className={compact ? "h-0.5" : "h-1"} />;
  }
  if (/^-{3,}$/.test(trimmed)) {
    return <hr key={index} className="border-[#eadfd8]" />;
  }
  const heading = trimmed.match(/^(#{1,4})\s+(.+)$/);
  if (heading) {
    return (
      <div key={index} className="font-semibold text-gray-900">
        {renderInlineMarkdown(heading[2] || "", `h-${index}`)}
      </div>
    );
  }
  const ordered = trimmed.match(/^(\d+)[.)]\s+(.+)$/);
  if (ordered) {
    return (
      <div key={index} className="flex gap-2">
        <span className="mt-0.5 inline-flex h-5 min-w-[1.25rem] shrink-0 items-center justify-center rounded-full border border-[#eadfd8] bg-white px-1 text-[11px] font-semibold text-[#8b5a45]">
          {ordered[1]}
        </span>
        <span className="min-w-0">{renderInlineMarkdown(ordered[2] || "", `o-${index}`)}</span>
      </div>
    );
  }
  const bullet = trimmed.match(/^[-*]\s+(.+)$/);
  if (bullet) {
    return (
      <div key={index} className="flex gap-2">
        <span className="mt-[0.65em] h-1.5 w-1.5 shrink-0 rounded-full bg-gray-400" />
        <span>{renderInlineMarkdown(bullet[1] || "", `b-${index}`)}</span>
      </div>
    );
  }
  return (
    <p key={index} className="whitespace-pre-wrap break-words">
      {renderInlineMarkdown(line, `p-${index}`)}
    </p>
  );
}

function isMarkdownTableStart(lines: string[], index: number) {
  if (index + 1 >= lines.length) {
    return false;
  }
  const header = splitMarkdownTableRow(lines[index]);
  const separator = splitMarkdownTableRow(lines[index + 1]);
  if (header.length < 2 || separator.length < 2) {
    return false;
  }
  return separator.every((cell) => /^:?-{3,}:?$/.test(cell.trim()));
}

function splitMarkdownTableRow(line?: string) {
  if (!line || !line.includes("|")) {
    return [];
  }
  const trimmed = line.trim().replace(/^\|/, "").replace(/\|$/, "");
  return trimmed.split("|").map((cell) => cell.trim());
}

function MarkdownTable({
  headerLine,
  separatorLine,
  rowLines,
  keyPrefix,
}: {
  headerLine: string;
  separatorLine: string;
  rowLines: string[];
  keyPrefix: string;
}) {
  const headers = splitMarkdownTableRow(headerLine);
  const alignments = splitMarkdownTableRow(separatorLine).map((cell) => {
    const trimmed = cell.trim();
    if (trimmed.startsWith(":") && trimmed.endsWith(":")) {
      return "text-center";
    }
    if (trimmed.endsWith(":")) {
      return "text-right";
    }
    return "text-left";
  });
  const rows = rowLines
    .map(splitMarkdownTableRow)
    .filter((row) => row.length > 0);

  return (
    <div className="my-2 max-w-full overflow-x-auto rounded-lg border border-[#e5e7eb] bg-white">
      <table className="min-w-full border-collapse text-xs leading-5">
        <thead className="bg-[#fafafa] text-gray-700">
          <tr>
            {headers.map((header, cellIndex) => (
              <th
                key={`${keyPrefix}-h-${cellIndex}`}
                className={`border-b border-[#e5e7eb] px-2.5 py-2 font-semibold ${alignments[cellIndex] || "text-left"}`}
              >
                {renderInlineMarkdown(header, `${keyPrefix}-h-${cellIndex}`)}
              </th>
            ))}
          </tr>
        </thead>
        <tbody className="divide-y divide-[#f0f0f0]">
          {rows.map((row, rowIndex) => (
            <tr key={`${keyPrefix}-r-${rowIndex}`} className="align-top">
              {headers.map((_, cellIndex) => (
                <td
                  key={`${keyPrefix}-r-${rowIndex}-${cellIndex}`}
                  className={`px-2.5 py-2 text-gray-800 ${alignments[cellIndex] || "text-left"}`}
                >
                  {renderInlineMarkdown(row[cellIndex] || "", `${keyPrefix}-r-${rowIndex}-${cellIndex}`)}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function renderInlineMarkdown(text: string, keyPrefix: string) {
  const nodes: React.ReactNode[] = [];
  const pattern = /(`[^`]+`|\*\*[^*]+\*\*|\*[^*]+\*)/g;
  let lastIndex = 0;
  let match: RegExpExecArray | null;
  while ((match = pattern.exec(text)) !== null) {
    if (match.index > lastIndex) {
      nodes.push(text.slice(lastIndex, match.index));
    }
    const token = match[0];
    const key = `${keyPrefix}-${match.index}`;
    if (token.startsWith("`")) {
      nodes.push(
        <code key={key} className="rounded bg-white px-1 py-0.5 font-mono text-xs text-gray-700">
          {token.slice(1, -1)}
        </code>,
      );
    } else if (token.startsWith("**")) {
      nodes.push(
        <strong key={key} className="font-semibold text-gray-900">
          {token.slice(2, -2)}
        </strong>,
      );
    } else {
      nodes.push(
        <em key={key} className="italic">
          {token.slice(1, -1)}
        </em>,
      );
    }
    lastIndex = pattern.lastIndex;
  }
  if (lastIndex < text.length) {
    nodes.push(text.slice(lastIndex));
  }
  return nodes;
}

export function TasksPanel({
  tasks,
  memberById,
  leaderMemberId,
}: {
  tasks: TeamTask[];
  memberById: Map<number, TeamMember>;
  leaderMemberId?: string;
}) {
  return (
    <section className="app-panel overflow-hidden">
      <div className="border-b border-[#f1e7e1] px-5 py-4">
        <h2 className="text-lg font-semibold text-gray-900">任务编排</h2>
        <p className="mt-1 text-sm text-gray-500">
          看任务从 ClawManager 进入哪个成员、执行到哪一步、最后产出或失败原因是什么。
        </p>
      </div>
      <div className="max-h-[640px] overflow-auto">
        {tasks.length === 0 ? (
          <div className="p-6 text-sm text-gray-500">暂无任务。</div>
        ) : (
          <ul className="space-y-4 p-5">
            {tasks.map((task) => {
              const target =
                memberById.get(task.target_member_id)?.member_key ||
                `#${task.target_member_id}`;
              const title = taskTitleText(task);
              const prompt = taskPromptText(task);
              const intent = taskIntentText(task.payload);
              const resultSummary =
                payloadText(task.result, ["summary", "result", "message"]) ||
                payloadText(task.payload, ["result", "answer"]);
              return (
                <li
                  key={task.id}
                  className="rounded-2xl border border-[#f1e1d8] bg-white p-4 shadow-sm"
                >
                  <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
                    <div className="min-w-0">
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="font-semibold text-gray-900">
                          #{task.id} {title}
                        </span>
                        <span
                          className={`inline-flex items-center rounded-full border px-2.5 py-0.5 text-xs font-medium ${statusStyle(task.status)}`}
                        >
                          {task.status}
                        </span>
                      </div>
                      <div className="mt-3 flex flex-wrap items-center gap-2 text-sm">
                        <MemberPill label="发起" value="ClawManager" />
                        <span className="text-gray-300">→</span>
                        <MemberPill
                          label={target === leaderMemberId ? "Leader" : "目标"}
                          value={target}
                        />
                        {intent && <MemberPill label="意图" value={intent} />}
                      </div>
                    </div>
                    <div className="shrink-0 text-right text-xs text-gray-500">
                      <div>创建 {formatDateTime(task.created_at)}</div>
                      {task.started_at && (
                        <div className="mt-1">
                          开始 {formatDateTime(task.started_at)}
                        </div>
                      )}
                      {task.finished_at && (
                        <div className="mt-1">
                          结束 {formatDateTime(task.finished_at)}
                        </div>
                      )}
                    </div>
                  </div>

                  {prompt && (
                    <div className="mt-4 rounded-xl bg-[#fff8f5] px-4 py-3 text-sm leading-6 text-gray-700">
                      {prompt}
                    </div>
                  )}

                  {task.error_message && (
                    <div className="mt-3 rounded-xl border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
                      {task.error_message}
                    </div>
                  )}

                  {resultSummary && (
                    <div className="mt-3 rounded-xl border border-green-200 bg-green-50 px-4 py-3 text-sm text-green-800">
                      {resultSummary}
                    </div>
                  )}

                  <details className="mt-3">
                    <summary className="cursor-pointer text-xs font-medium text-gray-500">
                      调试数据 · {task.message_id}
                    </summary>
                    <pre className="mt-2 max-h-40 overflow-auto rounded-lg bg-gray-50 p-3 text-xs text-gray-600">
                      {compactJson(task.payload)}
                    </pre>
                  </details>
                </li>
              );
            })}
          </ul>
        )}
      </div>
    </section>
  );
}

export function EventsPanel({
  events,
  memberById,
}: {
  events: TeamEvent[];
  memberById: Map<number, TeamMember>;
}) {
  return (
    <section className="app-panel overflow-hidden">
      <div className="border-b border-[#f1e7e1] px-5 py-4">
        <h2 className="text-lg font-semibold text-gray-900">协作时间线</h2>
        <p className="mt-1 text-sm text-gray-500">
          按时间显示成员收到、转派、开始、失败或完成任务的过程。
        </p>
      </div>
      <div className="max-h-[640px] overflow-auto">
        {events.length === 0 ? (
          <div className="p-6 text-sm text-gray-500">暂无事件。</div>
        ) : (
          <ol className="relative space-y-4 p-5 before:absolute before:left-7 before:top-6 before:h-[calc(100%-3rem)] before:w-px before:bg-[#eadfd8]">
            {events.map((event) => {
              const member = memberKeyFromEvent(event, memberById);
              const from = payloadText(event.payload, ["from"]);
              const to = payloadText(event.payload, ["to", "memberId"]);
              const intent = taskIntentText(event.payload);
              const summary =
                payloadText(event.payload, [
                  "summary",
                  "lastSummary",
                  "diagnostic",
                  "error",
                  "error_message",
                  "message",
                ]) || payloadText(event.payload, ["prompt", "title"]);
              return (
                <li key={event.id} className="relative pl-9">
                  <div
                    className={`absolute left-0 top-1 flex h-4 w-4 items-center justify-center rounded-full border-2 bg-white ${eventTone(event.event_type)}`}
                  />
                  <div className="rounded-2xl border border-[#f1e1d8] bg-white p-4 shadow-sm">
                    <div className="flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between">
                      <div>
                        <div className="flex flex-wrap items-center gap-2">
                          <span
                            className={`inline-flex items-center rounded-full border px-2.5 py-0.5 text-xs font-medium ${eventTone(event.event_type)}`}
                          >
                            {eventVerb(event.event_type)}
                          </span>
                          <span className="font-medium text-gray-900">
                            {member}
                          </span>
                          {event.task_id && (
                            <span className="text-sm text-gray-500">
                              任务 #{event.task_id}
                            </span>
                          )}
                        </div>
                        <div className="mt-2 flex flex-wrap items-center gap-2 text-sm">
                          {from && <MemberPill label="从" value={from} />}
                          {from && to && <span className="text-gray-300">→</span>}
                          {to && <MemberPill label="到" value={to} />}
                          {intent && <MemberPill label="意图" value={intent} />}
                        </div>
                      </div>
                      <div className="shrink-0 text-right text-xs text-gray-500">
                        <div>{formatDateTime(event.occurred_at || event.created_at)}</div>
                        {event.redis_stream_id && (
                          <div className="mt-1 font-mono">{event.redis_stream_id}</div>
                        )}
                      </div>
                    </div>

                    {summary && (
                      <div className="mt-3 rounded-xl bg-gray-50 px-4 py-3 text-sm leading-6 text-gray-700">
                        {summary}
                      </div>
                    )}

                    <details className="mt-3">
                      <summary className="cursor-pointer text-xs font-medium text-gray-500">
                        原始事件
                      </summary>
                      <pre className="mt-2 max-h-36 overflow-auto rounded-lg bg-gray-50 p-3 text-xs text-gray-600">
                        {compactJson(event.payload)}
                      </pre>
                    </details>
                  </div>
                </li>
              );
            })}
          </ol>
        )}
      </div>
    </section>
  );
}

function MemberPill({ label, value }: { label: string; value: string }) {
  return (
    <span className="inline-flex items-center gap-1 rounded-full border border-[#eadfd8] bg-white px-2.5 py-1 text-xs text-gray-600">
      <span className="text-gray-400">{label}</span>
      <span className="font-medium text-gray-800">{value}</span>
    </span>
  );
}

function MetaRow({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <dt className="text-gray-500">{label}</dt>
      <dd className="mt-1 break-all font-medium text-gray-900">{value}</dd>
    </div>
  );
}

export default TeamDetailPage;
