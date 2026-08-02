import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { formatTaskStateLabel, formatTaskSessionStateLabel } from "./state-labels";
import type { TaskState, TaskSessionState } from "@/lib/types/http";

const TEST_LOCALE_KEY = "__KANDEV_TEST_UI_LOCALE__";

beforeEach(() => {
  (globalThis as Record<string, unknown>)[TEST_LOCALE_KEY] = "zh-CN";
});

afterEach(() => {
  (globalThis as Record<string, unknown>)[TEST_LOCALE_KEY] = "en";
});

describe("formatTaskStateLabel", () => {
  it("maps known task states to human labels", () => {
    expect(formatTaskStateLabel("IN_PROGRESS")).toBe("进行中");
    expect(formatTaskStateLabel("WAITING_FOR_INPUT")).toBe("等待输入");
    expect(formatTaskStateLabel("TODO")).toBe("待办");
    expect(formatTaskStateLabel("COMPLETED")).toBe("已完成");
    expect(formatTaskStateLabel("FAILED")).toBe("失败");
    expect(formatTaskStateLabel("CANCELLED")).toBe("已取消");
    expect(formatTaskStateLabel("BLOCKED")).toBe("已阻塞");
    expect(formatTaskStateLabel("REVIEW")).toBe("审核");
    expect(formatTaskStateLabel("CREATED")).toBe("已创建");
    expect(formatTaskStateLabel("SCHEDULING")).toBe("正在调度");
  });

  it("returns the localized not-started label for null/undefined", () => {
    expect(formatTaskStateLabel(null)).toBe("未开始");
    expect(formatTaskStateLabel(undefined)).toBe("未开始");
  });

  it("falls back to the raw value for unknown states", () => {
    expect(formatTaskStateLabel("UNKNOWN_FUTURE" as TaskState)).toBe("UNKNOWN_FUTURE");
  });
});

describe("formatTaskSessionStateLabel", () => {
  it("maps known session states", () => {
    expect(formatTaskSessionStateLabel("RUNNING")).toBe("运行中");
    expect(formatTaskSessionStateLabel("STARTING")).toBe("正在启动");
    expect(formatTaskSessionStateLabel("WAITING_FOR_INPUT")).toBe("等待输入");
    expect(formatTaskSessionStateLabel("COMPLETED")).toBe("已完成");
    expect(formatTaskSessionStateLabel("FAILED")).toBe("失败");
    expect(formatTaskSessionStateLabel("CANCELLED")).toBe("已取消");
    expect(formatTaskSessionStateLabel("CREATED")).toBe("已创建");
  });

  it("returns empty string for null/undefined", () => {
    expect(formatTaskSessionStateLabel(null)).toBe("");
    expect(formatTaskSessionStateLabel(undefined)).toBe("");
  });

  it("falls back to the raw value for unknown states", () => {
    expect(formatTaskSessionStateLabel("UNKNOWN" as TaskSessionState)).toBe("UNKNOWN");
  });
});
