import type { AgencyAgentProfileKey } from "./agencyAgentProfiles";
import type { InstanceMode } from "../types/instance";
import type { TeamCommunicationMode } from "../types/team";

export type RuntimeType = "openclaw" | "hermes";
export type ResourcePresetKey = "small" | "medium" | "large" | "custom";

export type TeamMemberTemplateMember = {
  memberId: string;
  name: string;
  role: string;
  runtimeType: RuntimeType;
  instanceMode?: InstanceMode;
  description: string;
  resourcePreset: ResourcePresetKey;
  isLeader: boolean;
  cpuCores: number;
  memoryGb: number;
  diskGb: number;
  gpuEnabled: boolean;
  gpuCount: number;
  image: string;
  agentProfileKey?: AgencyAgentProfileKey;
};

export type TeamMemberTemplate = {
  id: string;
  name: string;
  teamName?: string;
  description?: string;
  communicationMode?: TeamCommunicationMode;
  source: "builtin" | "custom";
  members: TeamMemberTemplateMember[];
};

const baseMember = (
  overrides: Partial<TeamMemberTemplateMember>,
): TeamMemberTemplateMember => ({
  memberId: "worker",
  name: "",
  role: "developer",
  runtimeType: "openclaw",
  description: "",
  resourcePreset: "small",
  isLeader: false,
  cpuCores: 2,
  memoryGb: 4,
  diskGb: 20,
  gpuEnabled: false,
  gpuCount: 0,
  image: "",
  ...overrides,
});

export const BUILTIN_MEMBER_TEMPLATES: TeamMemberTemplate[] = [
  {
    id: "builtin-leader-worker",
    name: "Standard Two-Member Team",
    teamName: "research-team",
    description:
      "Leader-mediated Team: the Leader decomposes goals, coordinates members, and integrates results; the Worker executes implementation tasks and reports progress.",
    communicationMode: "leader_mediated",
    source: "builtin",
    members: [
      baseMember({
        memberId: "leader",
        name: "team-leader",
        role: "leader",
        description:
          "Team Leader / Agents Orchestrator: decomposes goals, coordinates members, maintains context, validates member outputs, and reports externally.",
        resourcePreset: "small",
        isLeader: true,
        agentProfileKey: "agency.agents-orchestrator",
      }),
      baseMember({
        memberId: "worker",
        name: "team-worker",
        role: "developer",
        description:
          "Senior Developer: executes implementation tasks assigned by the Leader, reports progress, lists changes, and escalates blockers.",
        agentProfileKey: "agency.senior-developer",
      }),
    ],
  },
  {
    id: "builtin-dev-qa-docs",
    name: "Delivery Three-Member Team",
    teamName: "delivery-team",
    description:
      "Delivery Team: the Leader decomposes and coordinates work, the Developer implements and integrates, and the Reviewer verifies tests, regressions, and delivery quality.",
    communicationMode: "leader_mediated",
    source: "builtin",
    members: [
      baseMember({
        memberId: "leader",
        name: "delivery-lead",
        role: "leader",
        description:
          "Agents Orchestrator: decomposes requirements, sets priorities, dispatches tasks, manages risks, and integrates results.",
        resourcePreset: "medium",
        isLeader: true,
        cpuCores: 4,
        memoryGb: 8,
        diskGb: 50,
        agentProfileKey: "agency.agents-orchestrator",
      }),
      baseMember({
        memberId: "developer",
        name: "developer",
        role: "developer",
        description:
          "Senior Developer: implements code, integrates interfaces, adds necessary tests, and provides reproducible delivery notes.",
        resourcePreset: "medium",
        cpuCores: 4,
        memoryGb: 8,
        diskGb: 50,
        agentProfileKey: "agency.senior-developer",
      }),
      baseMember({
        memberId: "reviewer",
        name: "reviewer",
        role: "reviewer",
        description:
          "Evidence Collector / Reviewer: verifies behavior, checks regressions, gathers evidence, reviews delivery items, and gives a PASS/FAIL verdict.",
        agentProfileKey: "agency.evidence-collector",
      }),
    ],
  },
  {
    id: "builtin-software-engineering-team",
    name: "Software-Engineering-Team",
    teamName: "software-engineering-team",
    description:
      "Software Engineering Team: the Leader owns goals, task breakdown, coordination, risk control, and final integration; PM, UI/UX, Frontend, Backend, Architect, QA, and Code Reviewer cover product, design, client, server, architecture, validation, and review.",
    communicationMode: "leader_mediated",
    source: "builtin",
    members: [
      baseMember({
        memberId: "leader",
        name: "engineering-lead",
        role: "leader",
        description:
          "Agents Orchestrator: owns goals, definition of done, task breakdown, dependency coordination, risk management, acceptance, and final decisions.",
        resourcePreset: "medium",
        isLeader: true,
        cpuCores: 4,
        memoryGb: 8,
        diskGb: 50,
        agentProfileKey: "agency.agents-orchestrator",
      }),
      baseMember({
        memberId: "pm",
        name: "product-manager",
        role: "product-manager",
        description:
          "Product Manager: owns requirements, product direction, PRD, user flows, feature boundaries, priorities, and acceptance criteria.",
        agentProfileKey: "agency.product-manager",
      }),
      baseMember({
        memberId: "ui-ux",
        name: "ui-ux-designer",
        role: "ui-ux-designer",
        description:
          "UI Designer: owns visual direction, UX, interaction states, component guidance, and implementable design handoff.",
        agentProfileKey: "agency.ui-designer",
      }),
      baseMember({
        memberId: "frontend",
        name: "frontend-engineer",
        role: "frontend-engineer",
        description:
          "Frontend Developer: owns frontend UI implementation, API integration, state management, interaction behavior, responsiveness, and accessibility.",
        resourcePreset: "medium",
        cpuCores: 4,
        memoryGb: 8,
        diskGb: 50,
        agentProfileKey: "agency.frontend-developer",
      }),
      baseMember({
        memberId: "backend",
        name: "backend-engineer",
        role: "backend-engineer",
        description:
          "Backend Architect: owns APIs, databases, permissions, queues, business logic, and server-side system capabilities.",
        resourcePreset: "medium",
        cpuCores: 4,
        memoryGb: 8,
        diskGb: 50,
        agentProfileKey: "agency.backend-architect",
      }),
      baseMember({
        memberId: "architect",
        name: "architect",
        role: "architect",
        description:
          "Software Architect: owns technical choices, system boundaries, availability, extensibility, technical standards, and evolution plans.",
        resourcePreset: "medium",
        cpuCores: 4,
        memoryGb: 8,
        diskGb: 50,
        agentProfileKey: "agency.software-architect",
      }),
      baseMember({
        memberId: "qa",
        name: "qa-engineer",
        role: "qa-engineer",
        description:
          "Evidence Collector: owns functional validation, regression checks, evidence gathering, reproduction notes, and acceptance verdicts.",
        agentProfileKey: "agency.evidence-collector",
      }),
      baseMember({
        memberId: "code-reviewer",
        name: "code-reviewer",
        role: "code-reviewer",
        description:
          "Code Reviewer: owns code review, architecture consistency, maintainability, test coverage, risk findings, and pre-merge quality gates.",
        agentProfileKey: "agency.code-reviewer",
      }),
    ],
  },
  {
    id: "builtin-product-discovery-team",
    name: "产品探索四成员",
    teamName: "product-discovery-team",
    communicationMode: "leader_mediated",
    description:
      "产品探索 Team：Leader 统筹目标与分工，产品经理澄清用户价值与需求，UI 设计师梳理交互方向，架构师评估技术可行性与边界。",
    source: "builtin",
    members: [
      baseMember({
        memberId: "leader",
        name: "discovery-lead",
        role: "leader",
        description:
          "Agents Orchestrator：统筹产品探索目标，分派产品、设计和架构任务，协调取舍，并汇总最终决策说明。",
        resourcePreset: "medium",
        isLeader: true,
        cpuCores: 4,
        memoryGb: 8,
        diskGb: 50,
        agentProfileKey: "agency.agents-orchestrator",
      }),
      baseMember({
        memberId: "pm",
        name: "product-manager",
        role: "product-manager",
        description:
          "Product Manager：定义用户问题、目标用户、功能边界、优先级、验收标准和上线影响。",
        agentProfileKey: "agency.product-manager",
      }),
      baseMember({
        memberId: "designer",
        name: "ui-designer",
        role: "ui-ux-designer",
        description:
          "UI Designer：把产品意图转化为交互结构、视觉方向、组件建议和可落地的设计交付。",
        agentProfileKey: "agency.ui-designer",
      }),
      baseMember({
        memberId: "architect",
        name: "solution-architect",
        role: "architect",
        description:
          "Software Architect：在开发前评估技术可行性、系统边界、依赖关系、技术风险和实现方案。",
        resourcePreset: "medium",
        cpuCores: 4,
        memoryGb: 8,
        diskGb: 50,
        agentProfileKey: "agency.software-architect",
      }),
    ],
  },
  {
    id: "builtin-fullstack-delivery-team",
    name: "全栈交付五成员",
    teamName: "fullstack-delivery-team",
    communicationMode: "leader_mediated",
    description:
      "全栈交付 Team：Leader 统筹交付，前端和后端负责实现，代码评审员检查风险，验收验证员在交付前确认行为证据。",
    source: "builtin",
    members: [
      baseMember({
        memberId: "leader",
        name: "delivery-lead",
        role: "leader",
        description:
          "Agents Orchestrator：拆解交付目标，分派前端、后端、评审和验证工作，管理风险并整合最终结果。",
        resourcePreset: "medium",
        isLeader: true,
        cpuCores: 4,
        memoryGb: 8,
        diskGb: 50,
        agentProfileKey: "agency.agents-orchestrator",
      }),
      baseMember({
        memberId: "frontend",
        name: "frontend-engineer",
        role: "frontend-engineer",
        description:
          "Frontend Developer：负责 UI 实现、前端状态、API 对接、响应式行为和前端质量检查。",
        resourcePreset: "medium",
        cpuCores: 4,
        memoryGb: 8,
        diskGb: 50,
        agentProfileKey: "agency.frontend-developer",
      }),
      baseMember({
        memberId: "backend",
        name: "backend-engineer",
        role: "backend-engineer",
        description:
          "Backend Architect：负责 API、数据契约、参数校验、持久化、权限、服务行为和后端验证说明。",
        resourcePreset: "medium",
        cpuCores: 4,
        memoryGb: 8,
        diskGb: 50,
        agentProfileKey: "agency.backend-architect",
      }),
      baseMember({
        memberId: "reviewer",
        name: "code-reviewer",
        role: "code-reviewer",
        description:
          "Code Reviewer：在最终验收前检查正确性、可维护性、安全性、回归风险和测试覆盖。",
        agentProfileKey: "agency.code-reviewer",
      }),
      baseMember({
        memberId: "qa",
        name: "qa-engineer",
        role: "qa-engineer",
        description:
          "Evidence Collector：用可复现步骤、截图或命令输出验证交付行为，并给出明确验收结论。",
        agentProfileKey: "agency.evidence-collector",
      }),
    ],
  },
  {
    id: "builtin-quality-gate-team",
    name: "质量验收四成员",
    teamName: "quality-gate-team",
    communicationMode: "leader_mediated",
    description:
      "质量验收 Team：Leader 统筹验收，代码评审员检查实现风险，API 测试员验证接口契约，验收验证员确认端到端证据。",
    source: "builtin",
    members: [
      baseMember({
        memberId: "leader",
        name: "quality-lead",
        role: "leader",
        description:
          "Agents Orchestrator：组织质量检查，分派评审和测试任务，跟踪问题并汇总是否可交付。",
        resourcePreset: "medium",
        isLeader: true,
        cpuCores: 4,
        memoryGb: 8,
        diskGb: 50,
        agentProfileKey: "agency.agents-orchestrator",
      }),
      baseMember({
        memberId: "reviewer",
        name: "code-reviewer",
        role: "code-reviewer",
        description:
          "Code Reviewer：识别正确性缺陷、边界条件、可维护性风险、安全问题和缺失测试。",
        agentProfileKey: "agency.code-reviewer",
      }),
      baseMember({
        memberId: "api-tester",
        name: "api-tester",
        role: "api-tester",
        description:
          "API Tester：验证接口行为、响应结构、鉴权失败、非法输入、不存在资源和延迟预期。",
        agentProfileKey: "agency.api-tester",
      }),
      baseMember({
        memberId: "qa",
        name: "evidence-collector",
        role: "qa-engineer",
        description:
          "Evidence Collector：收集直接验证证据，记录复现说明，并给出 PASS/FAIL 结论和必要修复建议。",
        agentProfileKey: "agency.evidence-collector",
      }),
    ],
  },
  {
    id: "builtin-api-integration-team",
    name: "API 集成五成员",
    teamName: "api-integration-team",
    communicationMode: "leader_mediated",
    description:
      "API 集成 Team：Leader 统筹集成范围，后端架构师定义服务契约，前端开发者对接接口，API 测试员验证行为，代码评审员检查集成风险。",
    source: "builtin",
    members: [
      baseMember({
        memberId: "leader",
        name: "integration-lead",
        role: "leader",
        description:
          "Agents Orchestrator：拆解集成目标，协调 API 契约、前端接入、测试、评审和最终交付。",
        resourcePreset: "medium",
        isLeader: true,
        cpuCores: 4,
        memoryGb: 8,
        diskGb: 50,
        agentProfileKey: "agency.agents-orchestrator",
      }),
      baseMember({
        memberId: "backend",
        name: "backend-architect",
        role: "backend-engineer",
        description:
          "Backend Architect：设计 API 契约、参数校验、持久化行为、错误处理和服务端集成细节。",
        resourcePreset: "medium",
        cpuCores: 4,
        memoryGb: 8,
        diskGb: 50,
        agentProfileKey: "agency.backend-architect",
      }),
      baseMember({
        memberId: "frontend",
        name: "frontend-engineer",
        role: "frontend-engineer",
        description:
          "Frontend Developer：对接 API，处理加载和错误状态，验证 UI 数据流并确认客户端行为。",
        resourcePreset: "medium",
        cpuCores: 4,
        memoryGb: 8,
        diskGb: 50,
        agentProfileKey: "agency.frontend-developer",
      }),
      baseMember({
        memberId: "api-tester",
        name: "api-tester",
        role: "api-tester",
        description:
          "API Tester：验证正常路径、参数错误、鉴权、响应结构，并提供可复现的 API 命令证据。",
        agentProfileKey: "agency.api-tester",
      }),
      baseMember({
        memberId: "reviewer",
        name: "code-reviewer",
        role: "code-reviewer",
        description:
          "Code Reviewer：检查集成改动的回归风险、可维护性、安全性、边界条件和缺失测试。",
        agentProfileKey: "agency.code-reviewer",
      }),
    ],
  },
];
