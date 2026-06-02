import React, { useCallback, useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import OpenClawConfigPlanSection, {
  type OpenClawInjectionMode,
} from "../../components/OpenClawConfigPlanSection";
import UserLayout from "../../components/UserLayout";
import {
  systemSettingsService,
  type SystemImageSetting,
} from "../../services/systemSettingsService";
import { teamService } from "../../services/teamService";
import type { CreateTeamRequest } from "../../types/team";
import type {
  OpenClawConfigCompilePreview,
  OpenClawConfigPlan,
} from "../../types/openclawConfig";

type EnvironmentRow = {
  id: string;
  name: string;
  value: string;
};

type TeamMemberDraft = {
  id: string;
  memberId: string;
  name: string;
  role: string;
  runtimeType: RuntimeType;
  description: string;
  resourcePreset: ResourcePresetKey;
  isLeader: boolean;
  cpuCores: number;
  memoryGb: number;
  diskGb: number;
  gpuEnabled: boolean;
  gpuCount: number;
  image: string;
};

type RuntimeType = "openclaw" | "hermes";
type ResourcePresetKey = "small" | "medium" | "large" | "custom";
type TeamMemberTemplateMember = Omit<TeamMemberDraft, "id">;
type TeamMemberTemplate = {
  id: string;
  name: string;
  teamName?: string;
  description?: string;
  source: "builtin" | "custom";
  members: TeamMemberTemplateMember[];
};

const RUNTIME_OPTIONS: Array<{ value: RuntimeType; label: string }> = [
  { value: "openclaw", label: "OpenClaw" },
  { value: "hermes", label: "Hermes" },
];

const RESOURCE_PRESETS: Record<
  Exclude<ResourcePresetKey, "custom">,
  { label: string; cpuCores: number; memoryGb: number; diskGb: number }
> = {
  small: { label: "小", cpuCores: 2, memoryGb: 4, diskGb: 20 },
  medium: { label: "中", cpuCores: 4, memoryGb: 8, diskGb: 50 },
  large: { label: "大", cpuCores: 8, memoryGb: 16, diskGb: 100 },
};

const ENV_NAME_PATTERN = /^[A-Za-z_][A-Za-z0-9_]*$/;
const CUSTOM_MEMBER_TEMPLATES_STORAGE_KEY = "clawmanager.team.memberTemplates.v1";

const BUILTIN_MEMBER_TEMPLATES: TeamMemberTemplate[] = [
  {
    id: "builtin-leader-worker",
    name: "标准双成员",
    teamName: "research-team",
    description: "标准 Leader-mediated Team：Leader 负责目标拆解、成员协调和结果汇总，Worker 负责执行实现任务并同步进展。",
    source: "builtin",
    members: [
      {
        memberId: "leader",
        name: "team-leader",
        role: "leader",
        runtimeType: "openclaw",
        description: "负责拆解目标、协调成员、汇总结果和对外回报。",
        resourcePreset: "small",
        isLeader: true,
        cpuCores: 2,
        memoryGb: 4,
        diskGb: 20,
        gpuEnabled: false,
        gpuCount: 0,
        image: "",
      },
      {
        memberId: "worker-1",
        name: "team-worker",
        role: "developer",
        runtimeType: "openclaw",
        description: "负责执行实现任务、提交进展并同步阻塞项。",
        resourcePreset: "small",
        isLeader: false,
        cpuCores: 2,
        memoryGb: 4,
        diskGb: 20,
        gpuEnabled: false,
        gpuCount: 0,
        image: "",
      },
    ],
  },
  {
    id: "builtin-dev-qa-docs",
    name: "研发验收三成员",
    teamName: "delivery-team",
    description: "研发交付 Team：Leader 负责拆解和协调，Developer 负责实现联调，Reviewer 负责测试验证、回归检查和交付复核。",
    source: "builtin",
    members: [
      {
        memberId: "leader",
        name: "delivery-lead",
        role: "leader",
        runtimeType: "openclaw",
        description: "负责需求拆解、优先级判断、任务派发和结果汇总。",
        resourcePreset: "medium",
        isLeader: true,
        cpuCores: 4,
        memoryGb: 8,
        diskGb: 50,
        gpuEnabled: false,
        gpuCount: 0,
        image: "",
      },
      {
        memberId: "developer",
        name: "developer",
        role: "developer",
        runtimeType: "openclaw",
        description: "负责代码实现、接口联调、必要的单元测试补充。",
        resourcePreset: "medium",
        isLeader: false,
        cpuCores: 4,
        memoryGb: 8,
        diskGb: 50,
        gpuEnabled: false,
        gpuCount: 0,
        image: "",
      },
      {
        memberId: "reviewer",
        name: "reviewer",
        role: "reviewer",
        runtimeType: "openclaw",
        description: "负责测试验证、回归检查、文档和交付清单复核。",
        resourcePreset: "small",
        isLeader: false,
        cpuCores: 2,
        memoryGb: 4,
        diskGb: 20,
        gpuEnabled: false,
        gpuCount: 0,
        image: "",
      },
    ],
  },
  {
    id: "builtin-software-engineering-team",
    name: "Software-Engineering-Team",
    teamName: "software-engineering-team",
    description:
      "软件工程 Team：Leader 负责目标、任务拆分、协调、风险控制和最终收口；PM 定义产品方向和优先级；UI/UX 负责体验与设计规范；Frontend、Backend、Architect、QA 和 Code Reviewer 分别负责界面、服务、架构、质量验证和代码审查。",
    source: "builtin",
    members: [
      {
        memberId: "leader",
        name: "engineering-lead",
        role: "leader",
        runtimeType: "openclaw",
        description:
          "负责目标管理、DoD 定义、任务拆分、依赖协调、风险治理、验收和最终决策。",
        resourcePreset: "medium",
        isLeader: true,
        cpuCores: 4,
        memoryGb: 8,
        diskGb: 50,
        gpuEnabled: false,
        gpuCount: 0,
        image: "",
      },
      {
        memberId: "pm",
        name: "product-manager",
        role: "product-manager",
        runtimeType: "openclaw",
        description:
          "负责需求收集、产品方向、PRD、Roadmap、用户流程、功能边界、优先级和验收标准。",
        resourcePreset: "small",
        isLeader: false,
        cpuCores: 2,
        memoryGb: 4,
        diskGb: 20,
        gpuEnabled: false,
        gpuCount: 0,
        image: "",
      },
      {
        memberId: "ui-ux",
        name: "ui-ux-designer",
        role: "ui-ux-designer",
        runtimeType: "openclaw",
        description:
          "负责页面视觉、用户体验、交互流程、Figma 设计稿、组件规范和 Design System。",
        resourcePreset: "small",
        isLeader: false,
        cpuCores: 2,
        memoryGb: 4,
        diskGb: 20,
        gpuEnabled: false,
        gpuCount: 0,
        image: "",
      },
      {
        memberId: "frontend",
        name: "frontend-engineer",
        role: "frontend-engineer",
        runtimeType: "openclaw",
        description:
          "负责 React/Vue 等前端页面开发、接口对接、状态管理、用户交互和性能优化。",
        resourcePreset: "medium",
        isLeader: false,
        cpuCores: 4,
        memoryGb: 8,
        diskGb: 50,
        gpuEnabled: false,
        gpuCount: 0,
        image: "",
      },
      {
        memberId: "backend",
        name: "backend-engineer",
        role: "backend-engineer",
        runtimeType: "openclaw",
        description:
          "负责 API、数据库、权限、消息队列、微服务、业务逻辑和系统能力实现。",
        resourcePreset: "medium",
        isLeader: false,
        cpuCores: 4,
        memoryGb: 8,
        diskGb: 50,
        gpuEnabled: false,
        gpuCount: 0,
        image: "",
      },
      {
        memberId: "architect",
        name: "architect",
        role: "architect",
        runtimeType: "openclaw",
        description:
          "负责技术选型、系统拆分、高可用、扩展性、技术规范和长期演进方案。",
        resourcePreset: "medium",
        isLeader: false,
        cpuCores: 4,
        memoryGb: 8,
        diskGb: 50,
        gpuEnabled: false,
        gpuCount: 0,
        image: "",
      },
      {
        memberId: "qa",
        name: "qa-engineer",
        role: "qa-engineer",
        runtimeType: "openclaw",
        description:
          "负责功能测试、自动化测试、回归测试、压测、Bug 管理和稳定性验证。",
        resourcePreset: "small",
        isLeader: false,
        cpuCores: 2,
        memoryGb: 4,
        diskGb: 20,
        gpuEnabled: false,
        gpuCount: 0,
        image: "",
      },
      {
        memberId: "code-reviewer",
        name: "code-reviewer",
        role: "code-reviewer",
        runtimeType: "openclaw",
        description:
          "负责代码审查、架构一致性、可维护性、测试覆盖、风险点、回归影响和合并前质量把关。",
        resourcePreset: "small",
        isLeader: false,
        cpuCores: 2,
        memoryGb: 4,
        diskGb: 20,
        gpuEnabled: false,
        gpuCount: 0,
        image: "",
      },
    ],
  },
];

const newDraftId = () =>
  `member-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 7)}`;

const defaultMember = (
  overrides?: Partial<TeamMemberDraft>,
): TeamMemberDraft => ({
  id: newDraftId(),
  memberId: "worker-1",
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

const imageOptionKey = (item: SystemImageSetting) =>
  item.id != null
    ? `image-${item.id}`
    : `${item.instance_type}:${item.image}`;

const normalizeMemberId = (value: string) =>
  value
    .trim()
    .toLowerCase()
    .replace(/[_\s]+/g, "-")
    .replace(/[^a-z0-9-]/g, "")
    .slice(0, 63);

const loadCustomMemberTemplates = (): TeamMemberTemplate[] => {
  try {
    if (typeof window === "undefined") {
      return [];
    }
    const raw = window.localStorage.getItem(CUSTOM_MEMBER_TEMPLATES_STORAGE_KEY);
    if (!raw) {
      return [];
    }
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) {
      return [];
    }
    return parsed
      .filter((item): item is TeamMemberTemplate => {
        return (
          item &&
          typeof item.id === "string" &&
          typeof item.name === "string" &&
          Array.isArray(item.members)
        );
      })
      .map((item) => ({
        ...item,
        source: "custom" as const,
      }));
  } catch {
    return [];
  }
};

const saveCustomMemberTemplates = (templates: TeamMemberTemplate[]) => {
  try {
    if (typeof window === "undefined") {
      return;
    }
    window.localStorage.setItem(
      CUSTOM_MEMBER_TEMPLATES_STORAGE_KEY,
      JSON.stringify(templates),
    );
  } catch {
    // localStorage can be unavailable in private browsing or strict webviews.
  }
};

const uniqueMemberId = (raw: string, usedIds: Set<string>, fallbackIndex: number) => {
  const fallback = `member-${fallbackIndex}`;
  const base = normalizeMemberId(raw) || fallback;
  let candidate = base;
  let suffix = 2;

  while (usedIds.has(candidate)) {
    const suffixText = `-${suffix}`;
    candidate = `${base.slice(0, 63 - suffixText.length)}${suffixText}`;
    suffix += 1;
  }

  usedIds.add(candidate);
  return candidate;
};

const nextWorkerMemberId = (members: TeamMemberDraft[]) => {
  const usedIds = new Set(
    members
      .map((member) => normalizeMemberId(member.memberId))
      .filter(Boolean),
  );
  let maxWorkerIndex = 0;

  usedIds.forEach((memberId) => {
    if (memberId === "worker") {
      maxWorkerIndex = Math.max(maxWorkerIndex, 1);
      return;
    }
    const match = memberId.match(/^worker-(\d+)$/);
    if (match) {
      maxWorkerIndex = Math.max(maxWorkerIndex, Number(match[1]));
    }
  });

  let candidateIndex = maxWorkerIndex + 1;
  let candidate = `worker-${candidateIndex}`;
  while (usedIds.has(candidate)) {
    candidateIndex += 1;
    candidate = `worker-${candidateIndex}`;
  }
  return candidate;
};

const CreateTeamPage: React.FC = () => {
  const navigate = useNavigate();
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [sharedStorageGb, setSharedStorageGb] = useState(10);
  const [storageClass, setStorageClass] = useState("");
  const [images, setImages] = useState<SystemImageSetting[]>([]);
  const [selectedImageKey, setSelectedImageKey] = useState("");
  const [customMemberTemplates, setCustomMemberTemplates] = useState<
    TeamMemberTemplate[]
  >(() => loadCustomMemberTemplates());
  const [selectedTemplateId, setSelectedTemplateId] = useState(
    BUILTIN_MEMBER_TEMPLATES[0]?.id || "",
  );
  const [templatePackageName, setTemplatePackageName] = useState("");
  const [templateNotice, setTemplateNotice] = useState<string | null>(null);
  const [members, setMembers] = useState<TeamMemberDraft[]>(() => [
    defaultMember({
      memberId: "leader",
      role: "leader",
      isLeader: true,
    }),
    defaultMember({
      memberId: "worker-1",
      role: "developer",
    }),
  ]);
  const [loadingImages, setLoadingImages] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [environmentRows, setEnvironmentRows] = useState<EnvironmentRow[]>([]);
  const [openClawInjectionMode, setOpenClawInjectionMode] =
    useState<OpenClawInjectionMode>("none");
  const [openClawBundleId, setOpenClawBundleId] = useState<number | undefined>();
  const [openClawResourceIds, setOpenClawResourceIds] = useState<number[]>([]);
  const [openClawPreview, setOpenClawPreview] =
    useState<OpenClawConfigCompilePreview | null>(null);
  const [openClawPreviewLoading, setOpenClawPreviewLoading] = useState(false);
  const [openClawPreviewError, setOpenClawPreviewError] = useState<string | null>(
    null,
  );

  const openClawImages = useMemo(
    () =>
      images.filter(
        (item) => item.instance_type === "openclaw" && item.is_enabled !== false,
      ),
    [images],
  );
  const hermesImages = useMemo(
    () =>
      images.filter(
        (item) => item.instance_type === "hermes" && item.is_enabled !== false,
      ),
    [images],
  );
  const selectedImage =
    openClawImages.find((item) => imageOptionKey(item) === selectedImageKey) ||
    openClawImages[0];
  const memberTemplates = useMemo(
    () => [...BUILTIN_MEMBER_TEMPLATES, ...customMemberTemplates],
    [customMemberTemplates],
  );
  const selectedTemplate = useMemo(
    () =>
      memberTemplates.find((template) => template.id === selectedTemplateId) ||
      memberTemplates[0],
    [memberTemplates, selectedTemplateId],
  );
  const imageOptionsForRuntime = useCallback(
    (runtimeType: RuntimeType) =>
      runtimeType === "hermes" ? hermesImages : openClawImages,
    [hermesImages, openClawImages],
  );

  useEffect(() => {
    const loadImages = async () => {
      try {
        setLoadingImages(true);
        const items = await systemSettingsService.getImageSettings();
        setImages(items);
      } catch {
        setImages([]);
      } finally {
        setLoadingImages(false);
      }
    };
    void loadImages();
  }, []);

  useEffect(() => {
    if (openClawImages.length === 0) {
      setSelectedImageKey("");
      return;
    }
    setSelectedImageKey((current) =>
      openClawImages.some((item) => imageOptionKey(item) === current)
        ? current
        : imageOptionKey(openClawImages[0]),
    );
  }, [openClawImages]);

  useEffect(() => {
    saveCustomMemberTemplates(customMemberTemplates);
  }, [customMemberTemplates]);

  useEffect(() => {
    if (
      memberTemplates.length > 0 &&
      !memberTemplates.some((template) => template.id === selectedTemplateId)
    ) {
      setSelectedTemplateId(memberTemplates[0].id);
    }
  }, [memberTemplates, selectedTemplateId]);

  useEffect(() => {
    setMembers((current) =>
      current.map((member) => {
        const options = imageOptionsForRuntime(member.runtimeType);
        const fallbackImage = options[0]?.image || "";
        const imageMatchesRuntime = options.some(
          (item) => item.image === member.image,
        );
        if ((!member.image || !imageMatchesRuntime) && fallbackImage) {
          return { ...member, image: fallbackImage };
        }
        return member;
      }),
    );
  }, [imageOptionsForRuntime]);

  const updateMember = (
    id: string,
    patch:
      | Partial<TeamMemberDraft>
      | ((current: TeamMemberDraft) => Partial<TeamMemberDraft>),
  ) => {
    setMembers((current) =>
      current.map((member) => {
        if (member.id !== id) {
          return member;
        }
        const nextPatch = typeof patch === "function" ? patch(member) : patch;
        return { ...member, ...nextPatch };
      }),
    );
  };

  const setMemberRuntimeType = (id: string, runtimeType: RuntimeType) => {
    const fallbackImage = imageOptionsForRuntime(runtimeType)[0]?.image || "";
    updateMember(id, { runtimeType, image: fallbackImage });
  };

  const applyResourcePreset = (id: string, preset: ResourcePresetKey) => {
    if (preset === "custom") {
      updateMember(id, { resourcePreset: "custom" });
      return;
    }
    const config = RESOURCE_PRESETS[preset];
    updateMember(id, {
      resourcePreset: preset,
      cpuCores: config.cpuCores,
      memoryGb: config.memoryGb,
      diskGb: config.diskGb,
    });
  };

  const setLeader = (id: string) => {
    setMembers((current) =>
      current.map((member) => ({
        ...member,
        isLeader: member.id === id,
        role: member.id === id ? "leader" : member.role === "leader" ? "developer" : member.role,
      })),
    );
  };

  const addMember = () => {
    setMembers((current) => [
      ...current,
      defaultMember({
        memberId: nextWorkerMemberId(current),
        image: selectedImage?.image || "",
      }),
    ]);
  };

  const removeMember = (id: string) => {
    setMembers((current) => {
      const next = current.filter((member) => member.id !== id);
      if (next.length > 0 && !next.some((member) => member.isLeader)) {
        return next.map((member, index) =>
          index === 0
            ? { ...member, isLeader: true, role: "leader" }
            : member,
        );
      }
      return next;
    });
  };

  const draftFromTemplateMember = (
    templateMember: TeamMemberTemplateMember,
    usedIds: Set<string>,
    index: number,
    isLeader: boolean,
  ): TeamMemberDraft => {
    const runtimeType = templateMember.runtimeType || "openclaw";
    const runtimeImages = imageOptionsForRuntime(runtimeType);
    const templateImageAvailable = runtimeImages.some(
      (item) => item.image === templateMember.image,
    );
    const image = templateImageAvailable
      ? templateMember.image
      : runtimeImages[0]?.image || templateMember.image || "";
    const role =
      isLeader || templateMember.role !== "leader"
        ? templateMember.role
        : "developer";

    return defaultMember({
      ...templateMember,
      memberId: uniqueMemberId(templateMember.memberId, usedIds, index),
      role: isLeader ? "leader" : role || "member",
      runtimeType,
      isLeader,
      image,
    });
  };

  const buildTemplateMembers = (
    template: TeamMemberTemplate,
    existingMembers: TeamMemberDraft[],
  ) => {
    const usedIds = new Set(
      existingMembers
        .map((member) => normalizeMemberId(member.memberId))
        .filter(Boolean),
    );
    const importedMembers: TeamMemberDraft[] = [];
    let leaderAssigned = existingMembers.some((member) => member.isLeader);

    template.members.forEach((templateMember, index) => {
      const shouldBeLeader = Boolean(templateMember.isLeader) && !leaderAssigned;
      if (shouldBeLeader) {
        leaderAssigned = true;
      }
      importedMembers.push(
        draftFromTemplateMember(templateMember, usedIds, index + 1, shouldBeLeader),
      );
    });

    if (!leaderAssigned && importedMembers.length > 0) {
      importedMembers[0] = {
        ...importedMembers[0],
        isLeader: true,
        role: "leader",
      };
    }

    return importedMembers;
  };

  const importMemberTemplate = (mode: "replace" | "append") => {
    if (!selectedTemplate) {
      return;
    }
    if (selectedTemplate.teamName) {
      setName(selectedTemplate.teamName);
    }
    if (selectedTemplate.description) {
      setDescription(selectedTemplate.description);
    }
    setMembers((current) => {
      const existingMembers = mode === "append" ? current : [];
      const importedMembers = buildTemplateMembers(selectedTemplate, existingMembers);
      return mode === "append" ? [...current, ...importedMembers] : importedMembers;
    });
    setTemplateNotice(
      mode === "append"
        ? `已追加模板包：${selectedTemplate.name}`
        : `已导入模板包：${selectedTemplate.name}`,
    );
    setError(null);
  };

  const buildTemplateFromCurrentMembers = (
    packageName: string,
    templateId?: string,
  ): TeamMemberTemplate | null => {
    const templateMembers = members.map<TeamMemberTemplateMember>((member, index) => ({
      memberId: normalizeMemberId(member.memberId) || `member-${index + 1}`,
      name: member.name.trim(),
      role: member.isLeader ? "leader" : member.role.trim() || "member",
      runtimeType: member.runtimeType,
      description: member.description.trim(),
      resourcePreset: member.resourcePreset,
      isLeader: member.isLeader,
      cpuCores: member.cpuCores,
      memoryGb: member.memoryGb,
      diskGb: member.diskGb,
      gpuEnabled: member.gpuEnabled,
      gpuCount: member.gpuEnabled ? member.gpuCount : 0,
      image: member.image.trim(),
    }));

    return {
      id:
        templateId ||
        `custom-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 7)}`,
      name: packageName,
      teamName: name.trim() || undefined,
      description: description.trim() || undefined,
      source: "custom",
      members: templateMembers,
    };
  };

  const saveCurrentMembersAsTemplate = () => {
    const packageName = templatePackageName.trim();
    if (!packageName) {
      setError("模板包名称不能为空");
      return;
    }
    if (members.length === 0) {
      setError("至少需要一个成员才能保存模板");
      return;
    }

    const template = buildTemplateFromCurrentMembers(packageName);
    if (!template) {
      return;
    }

    setCustomMemberTemplates((current) => [...current, template]);
    setSelectedTemplateId(template.id);
    setTemplatePackageName("");
    setTemplateNotice(`已保存模板包：${packageName}`);
    setError(null);
  };

  const updateSelectedTemplate = () => {
    if (!selectedTemplate || selectedTemplate.source !== "custom") {
      setError("只能编辑自定义模板，请先把内置模板另存为自定义模板");
      return;
    }
    if (members.length === 0) {
      setError("至少需要一个成员才能更新模板");
      return;
    }
    const packageName = templatePackageName.trim() || selectedTemplate.name;
    const updatedTemplate = buildTemplateFromCurrentMembers(
      packageName,
      selectedTemplate.id,
    );
    if (!updatedTemplate) {
      return;
    }

    setCustomMemberTemplates((current) =>
      current.map((template) =>
        template.id === selectedTemplate.id ? updatedTemplate : template,
      ),
    );
    setTemplatePackageName("");
    setTemplateNotice(`已更新模板包：${packageName}`);
    setError(null);
  };

  const deleteSelectedTemplate = () => {
    if (!selectedTemplate || selectedTemplate.source !== "custom") {
      return;
    }
    setCustomMemberTemplates((current) =>
      current.filter((template) => template.id !== selectedTemplate.id),
    );
    setSelectedTemplateId(BUILTIN_MEMBER_TEMPLATES[0]?.id || "");
    setTemplateNotice(`已删除模板包：${selectedTemplate.name}`);
  };

  const addEnvironmentRow = () => {
    setEnvironmentRows((current) => [
      ...current,
      {
        id: `env-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 7)}`,
        name: "",
        value: "",
      },
    ]);
  };

  const updateEnvironmentRow = (
    id: string,
    patch: Partial<Omit<EnvironmentRow, "id">>,
  ) => {
    setEnvironmentRows((current) =>
      current.map((row) => (row.id === id ? { ...row, ...patch } : row)),
    );
  };

  const removeEnvironmentRow = (id: string) => {
    setEnvironmentRows((current) => current.filter((row) => row.id !== id));
  };

  const buildEnvironmentOverridesPayload = () => {
    const overrides: Record<string, string> = {};
    const seenNames = new Set<string>();

    for (const row of environmentRows) {
      const envName = row.name.trim();
      const hasName = envName.length > 0;
      const hasValue = row.value.length > 0;

      if (!hasName && !hasValue) {
        continue;
      }
      if (!hasName) {
        return { error: "环境变量名称不能为空" };
      }
      if (!ENV_NAME_PATTERN.test(envName)) {
        return { error: `环境变量名称无效：${envName}` };
      }
      if (seenNames.has(envName)) {
        return { error: `环境变量名称重复：${envName}` };
      }
      seenNames.add(envName);
      overrides[envName] = row.value;
    }

    return {
      overrides: Object.keys(overrides).length > 0 ? overrides : undefined,
    };
  };

  const buildOpenClawConfigPlan = (): OpenClawConfigPlan | undefined => {
    if (openClawInjectionMode === "bundle" && openClawBundleId) {
      return { mode: "bundle", bundle_id: openClawBundleId };
    }
    if (openClawInjectionMode === "manual" && openClawResourceIds.length > 0) {
      return { mode: "manual", resource_ids: openClawResourceIds };
    }
    return undefined;
  };

  const handleOpenClawPreviewChange = useCallback(
    (
      preview: OpenClawConfigCompilePreview | null,
      state: { loading: boolean; error: string | null },
    ) => {
      setOpenClawPreview(preview);
      setOpenClawPreviewLoading(state.loading);
      setOpenClawPreviewError(state.error);
    },
    [],
  );

  const validationError = useMemo(() => {
    if (!name.trim()) {
      return "Team 名称不能为空";
    }
    if (members.length === 0) {
      return "至少需要一个成员";
    }
    if (members.filter((member) => member.isLeader).length !== 1) {
      return "必须指定且只能指定一个 Leader";
    }
    const memberIds = new Set<string>();
    for (const member of members) {
      const memberId = normalizeMemberId(member.memberId);
      if (!memberId) {
        return "成员 ID 不能为空";
      }
      if (memberIds.has(memberId)) {
        return `成员 ID 重复：${memberId}`;
      }
      memberIds.add(memberId);
      if (!member.image.trim()) {
        return `成员 ${memberId} 未选择镜像`;
      }
      if (member.cpuCores <= 0 || member.memoryGb <= 0 || member.diskGb <= 0) {
        return `成员 ${memberId} 的资源规格无效`;
      }
    }
    return null;
  }, [members, name]);

  const environmentDraft = useMemo(
    () => buildEnvironmentOverridesPayload(),
    [environmentRows],
  );
  const openClawPlanInvalid =
    (openClawInjectionMode === "bundle" &&
      (!openClawBundleId || Boolean(openClawPreviewError) || openClawPreviewLoading)) ||
    (openClawInjectionMode === "manual" &&
      (Boolean(openClawPreviewError) || openClawPreviewLoading)) ||
    openClawInjectionMode === "archive";
  const environmentOverrideNames = environmentDraft.overrides
    ? Object.keys(environmentDraft.overrides)
    : [];
  const resolvedChannelNames = (openClawPreview?.resolved_resources || [])
    .filter((resource) => resource.resource_type === "channel")
    .map((resource) => resource.name);

  const submitDisabled =
    submitting ||
    Boolean(validationError) ||
    Boolean(environmentDraft.error) ||
    openClawPlanInvalid;

  const handleSubmit = async (event: React.FormEvent) => {
    event.preventDefault();
    if (validationError) {
      setError(validationError);
      return;
    }
    if (environmentDraft.error) {
      setError(environmentDraft.error);
      return;
    }
    if (openClawPlanInvalid) {
      setError(
        openClawInjectionMode === "archive"
          ? "Team 创建暂不支持 Archive 导入，请选择手动/Bundle 或关闭注入。"
          : openClawPreviewError || "OpenClaw 注入配置尚未就绪",
      );
      return;
    }
    const openClawConfigPlan = buildOpenClawConfigPlan();
    const payload: CreateTeamRequest = {
      name: name.trim(),
      description: description.trim() || undefined,
      communication_mode: "leader_mediated",
      shared_storage_gb: sharedStorageGb,
      storage_class: storageClass.trim() || undefined,
      members: members.map((member) => ({
        member_id: normalizeMemberId(member.memberId),
        name: member.name.trim() || undefined,
        role: member.isLeader ? "leader" : member.role.trim() || "member",
        runtime_type: member.runtimeType,
        description: member.description.trim() || undefined,
        is_leader: member.isLeader,
        cpu_cores: member.cpuCores,
        memory_gb: member.memoryGb,
        disk_gb: member.diskGb,
        gpu_enabled: member.gpuEnabled,
        gpu_count: member.gpuEnabled ? member.gpuCount : 0,
        image_registry: member.image.trim(),
        environment_overrides: environmentDraft.overrides,
        openclaw_config_plan: openClawConfigPlan,
      })),
    };

    try {
      setSubmitting(true);
      setError(null);
      const created = await teamService.createTeam(payload);
      navigate(`/teams/${created.team.id}`);
    } catch (err: any) {
      setError(err.response?.data?.error || "创建 Team 失败");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <UserLayout title="创建 Team">
      <form onSubmit={handleSubmit} className="space-y-6">
        {error && (
          <div className="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
            {error}
          </div>
        )}

        <div className="grid grid-cols-1 gap-6 xl:grid-cols-[minmax(0,1fr)_360px]">
          <div className="space-y-6">
            <section className="app-panel p-6">
              <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
                <label className="block">
                  <span className="text-sm font-medium text-gray-700">
                    Team 名称
                  </span>
                  <input
                    value={name}
                    onChange={(event) => setName(event.target.value)}
                    placeholder="research-team"
                    className="mt-1 block w-full rounded-xl border border-[#eadfd8] px-3 py-2 text-sm focus:border-[#ef4444] focus:outline-none focus:ring-1 focus:ring-[#f3d2c2]"
                  />
                </label>
                <label className="block">
                  <span className="text-sm font-medium text-gray-700">
                    共享存储
                  </span>
                  <div className="mt-1 flex rounded-xl border border-[#eadfd8] bg-white">
                    <input
                      type="number"
                      min={1}
                      value={sharedStorageGb}
                      onChange={(event) =>
                        setSharedStorageGb(Math.max(1, Number(event.target.value)))
                      }
                      className="min-w-0 flex-1 rounded-l-xl border-0 px-3 py-2 text-sm focus:outline-none focus:ring-1 focus:ring-[#f3d2c2]"
                    />
                    <span className="flex items-center rounded-r-xl border-l border-[#eadfd8] bg-gray-50 px-3 text-sm text-gray-500">
                      GiB
                    </span>
                  </div>
                </label>
                <label className="block md:col-span-2">
                  <span className="text-sm font-medium text-gray-700">
                    描述
                  </span>
                  <textarea
                    value={description}
                    onChange={(event) => setDescription(event.target.value)}
                    rows={3}
                    className="mt-1 block w-full rounded-xl border border-[#eadfd8] px-3 py-2 text-sm focus:border-[#ef4444] focus:outline-none focus:ring-1 focus:ring-[#f3d2c2]"
                  />
                </label>
                <label className="block md:col-span-2">
                  <span className="text-sm font-medium text-gray-700">
                    StorageClass
                  </span>
                  <input
                    value={storageClass}
                    onChange={(event) => setStorageClass(event.target.value)}
                    placeholder="留空使用集群默认，或填 manual"
                    className="mt-1 block w-full rounded-xl border border-[#eadfd8] px-3 py-2 text-sm focus:border-[#ef4444] focus:outline-none focus:ring-1 focus:ring-[#f3d2c2]"
                  />
                </label>
              </div>
            </section>

            <section className="app-panel p-6">
              <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
                <div>
                  <h2 className="text-lg font-semibold text-gray-900">
                    成员公共注入
                  </h2>
                  <p className="mt-1 text-sm text-gray-500">
                    这些环境变量和 OpenClaw 配置会应用到本次创建的所有 Team 成员。
                  </p>
                </div>
                <span className="rounded-full bg-indigo-50 px-3 py-1 text-xs font-medium text-indigo-600">
                  可选
                </span>
              </div>

              <div className="mt-5 border-t border-[#f1e5df] pt-5">
                <div className="flex items-center justify-between gap-3">
                  <div>
                    <h3 className="text-sm font-semibold text-gray-900">
                      环境变量
                    </h3>
                    <p className="mt-1 text-sm text-gray-500">
                      用于成员 Pod 的额外运行参数，敏感值仍建议走后端 Secret/受控配置。
                    </p>
                  </div>
                  <button
                    type="button"
                    onClick={addEnvironmentRow}
                    className="inline-flex items-center justify-center rounded-xl border border-[#eadfd8] bg-white px-3 py-2 text-sm font-medium text-[#5f5957] hover:bg-[#fff8f5]"
                  >
                    添加变量
                  </button>
                </div>

                <div className="mt-4 space-y-3">
                  {environmentRows.length === 0 ? (
                    <div className="rounded-xl border border-dashed border-[#eadfd8] bg-white px-4 py-3 text-sm text-gray-500">
                      暂无额外环境变量。
                    </div>
                  ) : (
                    environmentRows.map((row) => (
                      <div
                        key={row.id}
                        className="grid grid-cols-1 gap-3 md:grid-cols-[minmax(0,220px)_minmax(0,1fr)_auto]"
                      >
                        <input
                          value={row.name}
                          onChange={(event) =>
                            updateEnvironmentRow(row.id, {
                              name: event.target.value,
                            })
                          }
                          placeholder="ENV_NAME"
                          className="rounded-xl border border-[#eadfd8] px-3 py-2 text-sm focus:border-[#ef4444] focus:outline-none focus:ring-1 focus:ring-[#f3d2c2]"
                        />
                        <input
                          value={row.value}
                          onChange={(event) =>
                            updateEnvironmentRow(row.id, {
                              value: event.target.value,
                            })
                          }
                          placeholder="value"
                          className="rounded-xl border border-[#eadfd8] px-3 py-2 text-sm focus:border-[#ef4444] focus:outline-none focus:ring-1 focus:ring-[#f3d2c2]"
                        />
                        <button
                          type="button"
                          onClick={() => removeEnvironmentRow(row.id)}
                          className="rounded-xl border border-red-200 bg-red-50 px-3 py-2 text-sm font-medium text-red-700 hover:bg-red-100"
                        >
                          删除
                        </button>
                      </div>
                    ))
                  )}
                  {environmentDraft.error && (
                    <p className="text-sm text-red-600">
                      {environmentDraft.error}
                    </p>
                  )}
                </div>
              </div>

              <div className="mt-6 border-t border-[#f1e5df] pt-5">
                <h3 className="text-sm font-semibold text-gray-900">
                  OpenClaw 配置
                </h3>
                <div className="mt-4">
                  <OpenClawConfigPlanSection
                    embedded
                    hideHeader
                    mode={openClawInjectionMode}
                    bundleId={openClawBundleId}
                    resourceIds={openClawResourceIds}
                    onModeChange={(nextMode) => {
                      setOpenClawInjectionMode(nextMode);
                      setOpenClawPreview(null);
                      setOpenClawPreviewError(null);
                      if (nextMode !== "bundle") {
                        setOpenClawBundleId(undefined);
                      }
                      if (nextMode !== "manual") {
                        setOpenClawResourceIds([]);
                      }
                    }}
                    onSelectionChange={({ bundleId, resourceIds }) => {
                      setOpenClawBundleId(bundleId);
                      setOpenClawResourceIds(resourceIds);
                    }}
                    onPreviewChange={handleOpenClawPreviewChange}
                  />
                </div>

                {openClawInjectionMode === "archive" && (
                  <p className="mt-3 rounded-lg border border-yellow-200 bg-yellow-50 px-3 py-2 text-sm text-yellow-700">
                    Team 创建暂不支持 Archive 导入。请使用 Bundle、手动资源选择，或关闭注入。
                  </p>
                )}
                {openClawPreviewLoading && (
                  <p className="mt-3 text-sm text-gray-500">正在检查注入配置...</p>
                )}
                {openClawPreviewError && (
                  <p className="mt-3 text-sm text-red-600">
                    {openClawPreviewError}
                  </p>
                )}
              </div>
            </section>

            <section className="app-panel p-6">
              <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
                <div>
                  <h2 className="text-lg font-semibold text-gray-900">
                    成员
                  </h2>
                  <p className="mt-1 text-sm text-gray-500">
                    当前 {members.length} 个成员，{members.filter((member) => member.isLeader).length} 个 Leader
                  </p>
                </div>
                <button
                  type="button"
                  onClick={addMember}
                  className="inline-flex items-center justify-center rounded-xl border border-[#eadfd8] bg-white px-4 py-2 text-sm font-medium text-[#5f5957] hover:bg-[#fff8f5]"
                >
                  <svg
                    className="mr-2 h-5 w-5"
                    fill="none"
                    viewBox="0 0 24 24"
                    stroke="currentColor"
                  >
                    <path
                      strokeLinecap="round"
                      strokeLinejoin="round"
                      strokeWidth={2}
                      d="M12 4v16m8-8H4"
                    />
                  </svg>
                  添加成员
                </button>
              </div>

              <div className="mt-5 rounded-xl border border-[#eadfd8] bg-[#fffaf6] p-4">
                <div className="grid grid-cols-1 gap-3 lg:grid-cols-[minmax(0,1fr)_auto_auto_auto] lg:items-end">
                  <label className="block">
                    <span className="text-sm font-medium text-gray-700">
                      选择模板包
                    </span>
                    <select
                      value={selectedTemplate?.id || ""}
                      onChange={(event) => setSelectedTemplateId(event.target.value)}
                      className="mt-1 block w-full rounded-xl border border-[#eadfd8] bg-white px-3 py-2 text-sm focus:border-[#ef4444] focus:outline-none focus:ring-1 focus:ring-[#f3d2c2]"
                    >
                      {memberTemplates.map((template) => (
                        <option key={template.id} value={template.id}>
                          {template.name} · {template.members.length} 成员
                        </option>
                      ))}
                    </select>
                  </label>
                  <button
                    type="button"
                    onClick={() => importMemberTemplate("replace")}
                    className="inline-flex items-center justify-center rounded-xl border border-[#ef4444] bg-white px-4 py-2 text-sm font-medium text-[#dc2626] hover:bg-[#fff1eb]"
                  >
                    替换导入
                  </button>
                  <button
                    type="button"
                    onClick={() => importMemberTemplate("append")}
                    className="inline-flex items-center justify-center rounded-xl border border-[#eadfd8] bg-white px-4 py-2 text-sm font-medium text-[#5f5957] hover:bg-[#fff8f5]"
                  >
                    追加导入
                  </button>
                  <button
                    type="button"
                    disabled={selectedTemplate?.source !== "custom"}
                    onClick={deleteSelectedTemplate}
                    className="inline-flex items-center justify-center rounded-xl border border-red-200 bg-white px-4 py-2 text-sm font-medium text-red-700 hover:bg-red-50 disabled:cursor-not-allowed disabled:opacity-50"
                  >
                    删除模板
                  </button>
                </div>

                {selectedTemplate && (
                  <>
                    <div className="mt-3 rounded-lg border border-[#eadfd8] bg-white px-3 py-2 text-sm">
                      <div className="flex flex-col gap-2 md:flex-row md:items-start md:justify-between">
                        <div>
                          <div className="text-xs text-gray-500">预设 Team 名称</div>
                          <div className="mt-1 font-medium text-gray-900">
                            {selectedTemplate.teamName || "不预设"}
                          </div>
                        </div>
                        <div className="max-w-2xl text-xs leading-5 text-gray-500">
                          {selectedTemplate.description || "不预设描述"}
                        </div>
                      </div>
                    </div>
                    <div className="mt-3 grid grid-cols-1 gap-2 md:grid-cols-2 xl:grid-cols-3">
                      {selectedTemplate.members.map((templateMember, index) => (
                        <div
                          key={`${selectedTemplate.id}-${templateMember.memberId}-${index}`}
                          className="rounded-lg border border-[#eadfd8] bg-white px-3 py-2 text-sm"
                        >
                          <div className="flex items-center justify-between gap-2">
                            <span className="font-medium text-gray-900">
                              {templateMember.memberId || `member-${index + 1}`}
                            </span>
                            <span className="text-xs text-gray-500">
                              {templateMember.isLeader ? "Leader" : templateMember.role}
                            </span>
                          </div>
                          <div className="mt-1 truncate text-xs text-gray-500">
                            {templateMember.runtimeType} ·{" "}
                            {templateMember.image || "默认镜像"} ·{" "}
                            {templateMember.cpuCores}C/{templateMember.memoryGb}G
                          </div>
                        </div>
                      ))}
                    </div>
                  </>
                )}

                <div className="mt-4 grid grid-cols-1 gap-3 md:grid-cols-[minmax(0,1fr)_auto_auto]">
                  <label className="block">
                    <span className="text-sm font-medium text-gray-700">
                      模板包名称
                    </span>
                    <input
                      value={templatePackageName}
                      onChange={(event) => setTemplatePackageName(event.target.value)}
                      placeholder={
                        selectedTemplate?.source === "custom"
                          ? selectedTemplate.name
                          : "例如：研发三人组"
                      }
                      className="mt-1 block w-full rounded-xl border border-[#eadfd8] bg-white px-3 py-2 text-sm focus:border-[#ef4444] focus:outline-none focus:ring-1 focus:ring-[#f3d2c2]"
                    />
                  </label>
                  <button
                    type="button"
                    onClick={saveCurrentMembersAsTemplate}
                    className="inline-flex items-center justify-center self-end rounded-xl border border-[#eadfd8] bg-white px-4 py-2 text-sm font-medium text-[#5f5957] hover:bg-[#fff8f5]"
                  >
                    保存当前为模板
                  </button>
                  <button
                    type="button"
                    disabled={selectedTemplate?.source !== "custom"}
                    onClick={updateSelectedTemplate}
                    className="inline-flex items-center justify-center self-end rounded-xl border border-[#ef4444] bg-white px-4 py-2 text-sm font-medium text-[#dc2626] hover:bg-[#fff1eb] disabled:cursor-not-allowed disabled:opacity-50"
                  >
                    更新模板
                  </button>
                </div>

                {templateNotice && (
                  <p className="mt-3 text-sm text-green-700">{templateNotice}</p>
                )}
              </div>

              <div className="mt-5 space-y-4">
                {members.map((member, index) => (
                  <div
                    key={member.id}
                    className="rounded-xl border border-[#eadfd8] bg-white p-4"
                  >
                    <div className="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
                      <label className="inline-flex items-center gap-2 text-sm font-medium text-gray-700">
                        <input
                          type="radio"
                          checked={member.isLeader}
                          onChange={() => setLeader(member.id)}
                        />
                        Team Leader
                      </label>
                      <button
                        type="button"
                        disabled={members.length <= 1}
                        onClick={() => removeMember(member.id)}
                        className="inline-flex items-center justify-center rounded-xl border border-red-200 bg-red-50 px-3 py-2 text-sm font-medium text-red-700 hover:bg-red-100 disabled:cursor-not-allowed disabled:opacity-50"
                      >
                        移除
                      </button>
                    </div>

                    <div className="mt-4 grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-5">
                      <label className="block">
                        <span className="text-sm font-medium text-gray-700">
                          成员 ID
                        </span>
                        <input
                          value={member.memberId}
                          onChange={(event) =>
                            updateMember(member.id, {
                              memberId: event.target.value,
                            })
                          }
                          onBlur={() =>
                            updateMember(member.id, (current) => ({
                              memberId:
                                normalizeMemberId(current.memberId) ||
                                `member-${index + 1}`,
                            }))
                          }
                          className="mt-1 block w-full rounded-xl border border-[#eadfd8] px-3 py-2 text-sm focus:border-[#ef4444] focus:outline-none focus:ring-1 focus:ring-[#f3d2c2]"
                        />
                      </label>
                      <label className="block">
                        <span className="text-sm font-medium text-gray-700">
                          显示名称
                        </span>
                        <input
                          value={member.name}
                          onChange={(event) =>
                            updateMember(member.id, { name: event.target.value })
                          }
                          placeholder={`${name || "team"}-${member.memberId}`}
                          className="mt-1 block w-full rounded-xl border border-[#eadfd8] px-3 py-2 text-sm focus:border-[#ef4444] focus:outline-none focus:ring-1 focus:ring-[#f3d2c2]"
                        />
                      </label>
                      <label className="block">
                        <span className="text-sm font-medium text-gray-700">
                          角色
                        </span>
                        <input
                          value={member.isLeader ? "leader" : member.role}
                          disabled={member.isLeader}
                          onChange={(event) =>
                            updateMember(member.id, { role: event.target.value })
                          }
                          className="mt-1 block w-full rounded-xl border border-[#eadfd8] px-3 py-2 text-sm focus:border-[#ef4444] focus:outline-none focus:ring-1 focus:ring-[#f3d2c2] disabled:bg-gray-50"
                        />
                      </label>
                      <label className="block">
                        <span className="text-sm font-medium text-gray-700">
                          Runtime
                        </span>
                        <select
                          value={member.runtimeType}
                          onChange={(event) =>
                            setMemberRuntimeType(
                              member.id,
                              event.target.value as RuntimeType,
                            )
                          }
                          className="mt-1 block w-full rounded-xl border border-[#eadfd8] px-3 py-2 text-sm focus:border-[#ef4444] focus:outline-none focus:ring-1 focus:ring-[#f3d2c2]"
                        >
                          {RUNTIME_OPTIONS.map((option) => (
                            <option key={option.value} value={option.value}>
                              {option.label}
                            </option>
                          ))}
                        </select>
                      </label>
                      <label className="block">
                        <span className="text-sm font-medium text-gray-700">
                          镜像
                        </span>
                        <select
                          value={member.image}
                          onChange={(event) =>
                            updateMember(member.id, { image: event.target.value })
                          }
                          className="mt-1 block w-full rounded-xl border border-[#eadfd8] px-3 py-2 text-sm focus:border-[#ef4444] focus:outline-none focus:ring-1 focus:ring-[#f3d2c2]"
                        >
                          {loadingImages ? (
                            <option value="">加载中...</option>
                          ) : imageOptionsForRuntime(member.runtimeType).length === 0 ? (
                            <option value="">暂无 {member.runtimeType} 镜像</option>
                          ) : (
                            imageOptionsForRuntime(member.runtimeType).map((item) => (
                              <option key={imageOptionKey(item)} value={item.image}>
                                {item.display_name || item.image}
                              </option>
                            ))
                          )}
                        </select>
                      </label>
                    </div>

                    <label className="mt-4 block">
                      <span className="text-sm font-medium text-gray-700">
                        职责描述
                      </span>
                      <textarea
                        value={member.description}
                        onChange={(event) =>
                          updateMember(member.id, {
                            description: event.target.value,
                          })
                        }
                        rows={3}
                        placeholder="说明这个成员负责什么，例如代码实现、测试验证、文档整理。"
                        className="mt-1 block w-full rounded-xl border border-[#eadfd8] px-3 py-2 text-sm focus:border-[#ef4444] focus:outline-none focus:ring-1 focus:ring-[#f3d2c2]"
                      />
                    </label>

                    <div className="mt-4">
                      <div className="text-sm font-medium text-gray-700">
                        资源预设
                      </div>
                      <div className="mt-2 grid grid-cols-2 gap-2 md:grid-cols-4">
                        {([
                          "small",
                          "medium",
                          "large",
                          "custom",
                        ] as ResourcePresetKey[]).map((preset) => (
                          <button
                            key={preset}
                            type="button"
                            onClick={() => applyResourcePreset(member.id, preset)}
                            className={`rounded-xl border px-3 py-2 text-sm font-medium ${
                              member.resourcePreset === preset
                                ? "border-[#ef4444] bg-[#fff1eb] text-[#dc2626]"
                                : "border-[#eadfd8] bg-white text-[#5f5957] hover:bg-[#fff8f5]"
                            }`}
                          >
                            {preset === "custom"
                              ? "自定义"
                              : `${RESOURCE_PRESETS[preset].label} · ${RESOURCE_PRESETS[preset].cpuCores}C/${RESOURCE_PRESETS[preset].memoryGb}G`}
                          </button>
                        ))}
                      </div>
                    </div>

                    <div className="mt-4 grid grid-cols-2 gap-4 md:grid-cols-5">
                      <NumberField
                        label="CPU"
                        value={member.cpuCores}
                        min={0.1}
                        step={0.1}
                        onChange={(value) =>
                          updateMember(member.id, {
                            cpuCores: value,
                            resourcePreset: "custom",
                          })
                        }
                      />
                      <NumberField
                        label="内存 GiB"
                        value={member.memoryGb}
                        min={1}
                        step={1}
                        onChange={(value) =>
                          updateMember(member.id, {
                            memoryGb: value,
                            resourcePreset: "custom",
                          })
                        }
                      />
                      <NumberField
                        label="磁盘 GiB"
                        value={member.diskGb}
                        min={1}
                        step={1}
                        onChange={(value) =>
                          updateMember(member.id, {
                            diskGb: value,
                            resourcePreset: "custom",
                          })
                        }
                      />
                      <label className="flex items-center gap-2 pt-7 text-sm font-medium text-gray-700">
                        <input
                          type="checkbox"
                          checked={member.gpuEnabled}
                          onChange={(event) =>
                            updateMember(member.id, {
                              gpuEnabled: event.target.checked,
                              gpuCount: event.target.checked
                                ? Math.max(1, member.gpuCount || 1)
                                : 0,
                            })
                          }
                        />
                        GPU
                      </label>
                      <NumberField
                        label="GPU 数"
                        value={member.gpuCount}
                        min={0}
                        step={1}
                        disabled={!member.gpuEnabled}
                        onChange={(value) =>
                          updateMember(member.id, { gpuCount: value })
                        }
                      />
                    </div>
                  </div>
                ))}
              </div>
            </section>
          </div>

          <aside className="space-y-6 xl:sticky xl:top-24 xl:self-start">
            <section className="app-panel p-6">
              <h2 className="text-lg font-semibold text-gray-900">摘要</h2>
              <dl className="mt-5 space-y-4 text-sm">
                <SummaryRow label="Team" value={name || "未命名"} />
                <SummaryRow
                  label="Leader"
                  value={
                    members.find((member) => member.isLeader)?.memberId ||
                    "未指定"
                  }
                />
                <SummaryRow label="成员数" value={`${members.length}`} />
                <SummaryRow
                  label="成员模板"
                  value={selectedTemplate?.name || "未选择"}
                />
                <SummaryRow
                  label="共享存储"
                  value={`${sharedStorageGb || 0} GiB`}
                />
                <SummaryRow
                  label="默认镜像"
                  value={selectedImage?.display_name || selectedImage?.image || "无"}
                />
                <SummaryRow
                  label="环境变量"
                  value={
                    environmentOverrideNames.length > 0
                      ? environmentOverrideNames.join(", ")
                      : "无"
                  }
                />
                <SummaryRow
                  label="OpenClaw 注入"
                  value={
                    openClawInjectionMode === "bundle"
                      ? openClawBundleId
                        ? `Bundle #${openClawBundleId}`
                        : "Bundle 未选择"
                      : openClawInjectionMode === "manual"
                        ? resolvedChannelNames.length > 0
                          ? resolvedChannelNames.join(", ")
                          : `${openClawResourceIds.length} 个资源`
                        : openClawInjectionMode === "archive"
                          ? "Archive 暂不支持"
                          : "关闭"
                  }
                />
              </dl>
              {(validationError || environmentDraft.error) && (
                <p className="mt-5 rounded-lg border border-yellow-200 bg-yellow-50 px-3 py-2 text-sm text-yellow-700">
                  {validationError || environmentDraft.error}
                </p>
              )}
            </section>

            <div className="flex gap-3">
              <button
                type="button"
                onClick={() => navigate("/teams")}
                className="app-button-secondary flex-1"
              >
                取消
              </button>
              <button
                type="submit"
                disabled={submitDisabled}
                className="app-button-primary flex-1 disabled:cursor-not-allowed disabled:opacity-50"
              >
                {submitting ? "创建中..." : "创建"}
              </button>
            </div>
          </aside>
        </div>
      </form>
    </UserLayout>
  );
};

function NumberField({
  label,
  value,
  min,
  step,
  disabled,
  onChange,
}: {
  label: string;
  value: number;
  min: number;
  step: number;
  disabled?: boolean;
  onChange: (value: number) => void;
}) {
  return (
    <label className="block">
      <span className="text-sm font-medium text-gray-700">{label}</span>
      <input
        type="number"
        value={value}
        min={min}
        step={step}
        disabled={disabled}
        onChange={(event) => onChange(Number(event.target.value))}
        className="mt-1 block w-full rounded-xl border border-[#eadfd8] px-3 py-2 text-sm focus:border-[#ef4444] focus:outline-none focus:ring-1 focus:ring-[#f3d2c2] disabled:bg-gray-50"
      />
    </label>
  );
}

function SummaryRow({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <dt className="text-gray-500">{label}</dt>
      <dd className="mt-1 break-words font-medium text-gray-900">{value}</dd>
    </div>
  );
}

export default CreateTeamPage;
