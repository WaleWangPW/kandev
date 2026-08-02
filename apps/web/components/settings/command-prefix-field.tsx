"use client";

import { Input } from "@kandev/ui/input";
import { Label } from "@kandev/ui/label";
import type { ProfileFormData } from "@/components/settings/profile-form-fields";
import { translate } from "@/lib/i18n/locale";

export function CommandPrefixField({
  profile,
  baselineProfile,
  onChange,
}: {
  profile: ProfileFormData;
  baselineProfile?: ProfileFormData;
  onChange: (patch: Partial<ProfileFormData>) => void;
}) {
  return (
    <div
      className="space-y-2"
      data-settings-dirty={
        Boolean(baselineProfile) &&
        (profile.command_prefix ?? "") !== (baselineProfile?.command_prefix ?? "")
      }
      data-settings-dirty-level="container"
    >
      <Label htmlFor="profile-command-prefix">{translate("Command prefix")}</Label>
      <Input
        id="profile-command-prefix"
        data-testid="command-prefix-input"
        value={profile.command_prefix ?? ""}
        onChange={(event) => onChange({ command_prefix: event.target.value })}
        placeholder="e.g. greywall --"
      />
      <p className="text-xs text-muted-foreground">
        将这些令牌添加到智能体启动命令前，使其通过隔离启动器运行（例如{" "}
        <code>greywall --</code>）。该值会按 Shell 令牌解析。留空则直接运行智能体。仅适用于
        ACP 会话；方案使用 TUI 直通时无效。
      </p>
    </div>
  );
}
