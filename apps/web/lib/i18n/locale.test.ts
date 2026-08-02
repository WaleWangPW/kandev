import { afterEach, describe, expect, it } from "vitest";
import { getUiLocale, translate } from "./locale";

const TEST_LOCALE_KEY = "__KANDEV_TEST_UI_LOCALE__";

afterEach(() => {
  (globalThis as Record<string, unknown>)[TEST_LOCALE_KEY] = "en";
  globalThis.localStorage.clear();
});

describe("translate", () => {
  it("translates the standard workflow labels without changing unknown names", () => {
    expect(translate("Backlog", "zh-CN")).toBe("待办");
    expect(translate("In Progress", "zh-CN")).toBe("进行中");
    expect(translate("Review", "zh-CN")).toBe("审核");
    expect(translate("Done", "zh-CN")).toBe("已完成");
    expect(translate("客户上线检查", "zh-CN")).toBe("客户上线检查");
  });

  it("keeps the original text when English is selected", () => {
    expect(translate("New Task", "en")).toBe("New Task");
  });

  it("defaults to Simplified Chinese outside the test-only locale override", () => {
    delete (globalThis as Record<string, unknown>)[TEST_LOCALE_KEY];
    expect(getUiLocale()).toBe("zh-CN");
  });
});
