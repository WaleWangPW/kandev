"use client";

import { IconCopy, IconPlus } from "@tabler/icons-react";
import { Badge } from "@kandev/ui/badge";
import { Button } from "@kandev/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@kandev/ui/tooltip";
import { cn } from "@/lib/utils";
import {
  BUILT_IN_LAYOUT_PROFILES,
  getBuiltInLayoutOverride,
  getBuiltInLayoutOverrideSourceId,
  getLayoutProfileCompatibility,
  isBuiltInLayoutOverride,
  resolveEffectiveDefaultLayout,
  type BuiltInLayoutProfileId,
} from "@/lib/layout/layout-profiles";
import type { SavedLayout } from "@/lib/types/http";
import { translate } from "@/lib/i18n/locale";

export type LayoutProfileSelection =
  | { kind: "built-in"; id: BuiltInLayoutProfileId }
  | { kind: "custom"; id: string };

type LayoutProfileListProps = {
  profiles: SavedLayout[];
  selection: LayoutProfileSelection;
  onSelect: (selection: LayoutProfileSelection) => void;
  onCreate: () => void;
  onDuplicate: () => void;
};

function isSelected(
  selection: LayoutProfileSelection,
  kind: LayoutProfileSelection["kind"],
  id: string,
) {
  return selection.kind === kind && selection.id === id;
}

const profileButtonClass =
  "flex min-h-11 w-full cursor-pointer items-start justify-between gap-2 rounded-md border px-3 py-2 text-left transition-colors";

function ProfileAction({
  label,
  help,
  variant,
  testId,
  onClick,
  children,
}: {
  label: string;
  help: string;
  variant?: "default" | "outline";
  testId: string;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          type="button"
          size="sm"
          variant={variant}
          className="min-h-11 cursor-pointer sm:min-h-8"
          aria-label={label}
          onClick={onClick}
          data-testid={testId}
        >
          {children}
        </Button>
      </TooltipTrigger>
      <TooltipContent>{help}</TooltipContent>
    </Tooltip>
  );
}

function BuiltInProfileList({
  profiles,
  selection,
  effectiveBuiltInId,
  onSelect,
}: {
  profiles: SavedLayout[];
  selection: LayoutProfileSelection;
  effectiveBuiltInId: BuiltInLayoutProfileId | null;
  onSelect: (selection: LayoutProfileSelection) => void;
}) {
  return (
    <div className="space-y-1.5">
      <h4 className="text-xs font-medium uppercase text-muted-foreground">{translate("Built-in")}</h4>
      {BUILT_IN_LAYOUT_PROFILES.map((profile) => {
        const override = getBuiltInLayoutOverride(profiles, profile.id);
        return (
          <button
            key={profile.id}
            type="button"
            className={cn(
              profileButtonClass,
              isSelected(selection, "built-in", profile.id)
                ? "border-primary bg-primary/5"
                : "hover:bg-muted/50",
            )}
            onClick={() => onSelect({ kind: "built-in", id: profile.id })}
            data-testid={`layout-profile-built-in-${profile.id}`}
          >
            <span className="flex min-w-0 flex-1 flex-col gap-1">
              <span className="flex min-w-0 flex-wrap items-center justify-between gap-1">
                <span className="text-sm font-medium">{translate(profile.name)}</span>
                <span className="flex flex-wrap justify-end gap-1">
                  <Badge variant="outline">{translate("Built-in")}</Badge>
                  {override && <Badge variant="secondary">{translate("Customized")}</Badge>}
                  {effectiveBuiltInId === profile.id && <Badge variant="secondary">{translate("Default")}</Badge>}
                </span>
              </span>
              <span className="text-xs text-muted-foreground">{translate(profile.description)}</span>
            </span>
          </button>
        );
      })}
    </div>
  );
}

export function LayoutProfileList({
  profiles,
  selection,
  onSelect,
  onCreate,
  onDuplicate,
}: LayoutProfileListProps) {
  const effectiveDefault = resolveEffectiveDefaultLayout(profiles);
  const effectiveBuiltInId =
    effectiveDefault.source === "built-in"
      ? effectiveDefault.profile.id
      : getBuiltInLayoutOverrideSourceId(effectiveDefault.profile);
  const customProfiles = profiles.filter((profile) => !isBuiltInLayoutOverride(profile));
  return (
    <aside className="min-w-0 space-y-3" aria-label={translate("Layout profiles")}>
      <div className="flex flex-wrap gap-2">
        <ProfileAction
          label={translate("New layout")}
          help={translate("Create a new editable layout based on Default.")}
          onClick={onCreate}
          testId="layout-profile-create"
        >
          <IconPlus className="mr-1.5 h-4 w-4" /> {translate("New layout")}
        </ProfileAction>
        <ProfileAction
          label={translate("Duplicate layout")}
          help={translate("Create a separate custom copy of the selected layout.")}
          variant="outline"
          onClick={onDuplicate}
          testId="layout-profile-duplicate"
        >
          <IconCopy className="mr-1.5 h-4 w-4" /> {translate("Duplicate")}
        </ProfileAction>
      </div>

      <BuiltInProfileList
        profiles={profiles}
        selection={selection}
        effectiveBuiltInId={effectiveBuiltInId}
        onSelect={onSelect}
      />

      <div className="space-y-1.5">
        <h4 className="text-xs font-medium uppercase text-muted-foreground">{translate("Custom")}</h4>
        {customProfiles.length === 0 && (
          <p className="py-3 text-sm text-muted-foreground">{translate("No custom profiles")}</p>
        )}
        {customProfiles.map((profile) => {
          const compatibility = getLayoutProfileCompatibility(profile);
          return (
            <button
              key={profile.id}
              type="button"
              className={cn(
                profileButtonClass,
                isSelected(selection, "custom", profile.id)
                  ? "border-primary bg-primary/5"
                  : "hover:bg-muted/50",
              )}
              onClick={() => onSelect({ kind: "custom", id: profile.id })}
              data-testid={`layout-profile-custom-${profile.id}`}
            >
              <span className="min-w-0 truncate text-sm font-medium">{profile.name}</span>
              <span className="flex shrink-0 gap-1">
                {effectiveDefault.source === "custom" &&
                  effectiveDefault.profile.id === profile.id && (
                    <Badge variant="secondary">{translate("Default")}</Badge>
                  )}
                {compatibility.status === "legacy" && <Badge variant="outline">{translate("Unavailable")}</Badge>}
              </span>
            </button>
          );
        })}
      </div>
    </aside>
  );
}
