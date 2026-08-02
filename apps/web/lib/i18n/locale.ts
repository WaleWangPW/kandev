import { useCallback, useSyncExternalStore } from "react";

export const UI_LOCALES = ["zh-CN", "en"] as const;
export type UiLocale = (typeof UI_LOCALES)[number];

const STORAGE_KEY = "kandev.ui.locale";
const LOCALE_CHANGE_EVENT = "kandev:ui-locale-change";
const DEFAULT_LOCALE: UiLocale = "zh-CN";

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
};

function isUiLocale(value: string | null): value is UiLocale {
  return value !== null && UI_LOCALES.includes(value as UiLocale);
}

export function getUiLocale(): UiLocale {
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
