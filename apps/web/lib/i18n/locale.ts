import { useCallback, useSyncExternalStore } from "react";

export const UI_LOCALES = ["zh-CN", "en"] as const;
export type UiLocale = (typeof UI_LOCALES)[number];

const STORAGE_KEY = "kandev.ui.locale";
const LOCALE_CHANGE_EVENT = "kandev:ui-locale-change";
const DEFAULT_LOCALE: UiLocale = "zh-CN";
const TEST_LOCALE_KEY = "__KANDEV_TEST_UI_LOCALE__";

const ZH_CN: Record<string, string> = {
  Home: "主页",
  Inbox: "收件箱",
  "New Task": "新建任务",
  "Quick Chat": "快速对话",
  Settings: "设置",
  "Close settings": "关闭设置",
  Stats: "统计",
  "Improve Kandev": "改进 Kandev",
  "What's new": "更新内容",
  Kanban: "看板",
  Office: "协作中心",
  Backlog: "待办",
  Todo: "待办",
  "To do": "待办",
  "In Progress": "进行中",
  "In progress": "进行中",
  "In Review": "审核中",
  Review: "审核",
  Done: "已完成",
  Completed: "已完成",
  Blocked: "已阻塞",
  Cancelled: "已取消",
  Failed: "失败",
  Created: "已创建",
  Scheduling: "正在调度",
  Starting: "正在启动",
  Running: "运行中",
  Idle: "空闲",
  "Waiting for input": "等待输入",
  "Not started": "未开始",
  Filter: "筛选",
  Status: "状态",
  Priority: "优先级",
  Critical: "紧急",
  High: "高",
  Medium: "中",
  Low: "低",
  None: "无",
  "Over WIP limit": "超过进行中任务上限",
  "All tasks": "全部任务",
  "Search tasks": "搜索任务",
  "Agent profile no longer exists": "智能体配置已不存在",
  "This agent has stopped.": "此智能体已停止。",
  Resume: "继续",
  "Resuming...": "正在继续…",
  "Starting...": "正在启动…",
  "Start fresh session": "新开会话",
  "Loading older messages...": "正在加载较早消息…",
  "Load older messages": "加载较早消息",
  "Loading conversation...": "正在加载会话…",
  "No messages yet. Start the conversation!": "暂时没有消息，开始对话吧！",
  New: "新消息",
  "New messages": "新消息",
  "Show details": "显示详情",
  "Resume session": "继续会话",
  "stopped with an error": "因错误而停止",
  "The agent stopped with an error. Resume to retry the same conversation, or start a fresh session.":
    "智能体因错误停止。可继续以重试当前会话，或新开会话。",
  Upload: "上传",
  "More options": "更多选项",
  Chat: "对话",
  Files: "文件",
  Changes: "变更",
  Terminal: "终端",
  Plan: "计划",
  Browser: "浏览器",
  Details: "详情",
  "Hide details": "隐藏详情",
  "Show preparation details": "显示环境准备详情",
  "Hide preparation details": "隐藏环境准备详情",
  "Preparing...": "正在准备…",
  "Environment prepared": "环境已准备就绪",
  "Environment prepared with warnings": "环境已准备就绪，但有警告",
  "Environment prepared on a fresh sandbox": "已在新的隔离环境中完成准备",
  "Environment preparation failed": "环境准备失败",
  "Environment setup failed": "环境准备失败",
  "Hide previous agent error": "隐藏上一次智能体错误",
  "Cancel Turn": "取消本轮",
  "Commit Changes": "提交变更",
  Push: "推送",
  Pull: "拉取",
  "Create PR": "创建 PR",
  Rebase: "变基",
  "New Agent": "新建智能体",
  "Create Subtask": "创建子任务",
  "Add Browser Panel": "添加浏览器面板",
  "Add Terminal Panel": "添加终端面板",
  "Add Plan Panel": "添加计划面板",
  "Add Changes Panel": "添加变更面板",
  Merge: "合并",
  "Create File": "创建文件",
  Workspace: "工作区",
  Panels: "面板",
  Agent: "智能体",
  Git: "Git",
  "New issue": "新建任务",
  "Sub-issue of": "子任务，父任务为",
  "Task title": "任务标题",
  "Add description...": "添加任务说明…",
  "Discard Draft": "放弃草稿",
  "Task created": "任务已创建",
  "Failed to create issue": "创建任务失败",
  "Creating...": "正在创建…",
  "Create Task": "创建任务",
  "Select a project to create a task": "请先选择项目",
  "Add a title to create a task": "请先填写任务标题",
  Assignee: "执行人",
  Unassigned: "未分配",
  Project: "项目",
  "No project": "未选择项目",
  For: "分配给",
  in: "所属",
  "Hide reviewer": "隐藏验收人",
  "Add reviewer": "添加验收人",
  "Hide approver": "隐藏审批人",
  "Add approver": "添加审批人",
  Reviewer: "验收人",
  Approver: "审批人",
  "No grouping": "不分组",
  "Group by": "分组方式",
  Parent: "父任务",
  Updated: "最近更新",
  "Sort by": "排序方式",
  Sort: "排序",
  Title: "标题",
  Asc: "升序",
  Desc: "降序",
  Appearance: "外观",
  Layouts: "布局",
  Notifications: "通知",
  Editors: "编辑器",
  "Keyboard Shortcuts": "快捷键",
  "Task Actions": "任务操作",
  General: "通用",
  Workspaces: "工作区",
  Repositories: "仓库",
  Workflows: "工作流",
  Integrations: "集成",
  Automations: "自动化",
  Agents: "智能体",
  Prompts: "提示词",
  "Voice Mode": "语音模式",
  "Utility Agents": "工具智能体",
  Executors: "执行器",
  Secrets: "机密信息",
  "External MCP": "外部 MCP",
  Plugins: "插件",
  System: "系统",
  Active: "当前",
  Enabled: "已启用",
  "Feature Toggles": "功能开关",
  Database: "数据库",
  Backups: "备份",
  Storage: "存储",
  Logs: "日志",
  Updates: "更新",
  About: "关于",
  Licenses: "许可证",
  Users: "用户",
  Account: "账户",
  "Profile & Password": "资料与密码",
  "API Tokens": "API 令牌",
  "Theme, metrics, and changes panel preferences": "主题、指标和变更面板偏好设置",
  "Task workbench layout profiles and defaults": "任务工作台布局方案与默认值",
  "Shell, terminal fonts, and link behavior": "Shell、终端字体与链接行为",
  "Providers and notification events": "通知服务与触发事件",
  "Editor integrations and defaults": "编辑器集成与默认设置",
  "Chat input and command shortcuts": "对话输入和命令快捷键",
  "MCP task defaults, archive safeguards, and transcript preferences": "MCP 任务默认项、归档保护与会话记录偏好",
};

function isUiLocale(value: string | null): value is UiLocale {
  return value !== null && UI_LOCALES.includes(value as UiLocale);
}

function getTestLocale(): UiLocale | null {
  const value = (globalThis as Record<string, unknown>)[TEST_LOCALE_KEY];
  return typeof value === "string" && isUiLocale(value) ? value : null;
}

export function getUiLocale(): UiLocale {
  const testLocale = getTestLocale();
  if (testLocale) return testLocale;
  if (typeof window === "undefined") return DEFAULT_LOCALE;
  try {
    const stored = window.localStorage.getItem(STORAGE_KEY);
    return isUiLocale(stored) ? stored : DEFAULT_LOCALE;
  } catch {
    return DEFAULT_LOCALE;
  }
}

export function setUiLocale(locale: UiLocale): void {
  if (typeof window === "undefined") return;
  if (getUiLocale() === locale) return;
  try {
    window.localStorage.setItem(STORAGE_KEY, locale);
  } catch {
    // A private-mode or quota failure must not block the one-session switch.
  }
  window.dispatchEvent(new Event(LOCALE_CHANGE_EVENT));
  // Some existing display helpers are deliberately framework-agnostic and do
  // not subscribe to React state. A same-route reload ensures that every
  // stored state label is rendered in the selected locale without changing
  // task, workflow, or session data.
  window.location.reload();
}

function subscribe(onStoreChange: () => void): () => void {
  window.addEventListener(LOCALE_CHANGE_EVENT, onStoreChange);
  window.addEventListener("storage", onStoreChange);
  return () => {
    window.removeEventListener(LOCALE_CHANGE_EVENT, onStoreChange);
    window.removeEventListener("storage", onStoreChange);
  };
}

export function translate(value: string, locale = getUiLocale()): string {
  return locale === "zh-CN" ? (ZH_CN[value] ?? value) : value;
}

export function useI18n() {
  const locale = useSyncExternalStore(subscribe, getUiLocale, () => DEFAULT_LOCALE);
  const t = useCallback((value: string) => translate(value, locale), [locale]);
  const setLocale = useCallback((nextLocale: UiLocale) => setUiLocale(nextLocale), []);
  return { locale, t, setLocale };
}
