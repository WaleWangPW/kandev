import {
  IconArchive,
  IconBell,
  IconCommand,
  IconCode,
  IconLayoutDashboard,
  IconPalette,
  IconTerminal2,
} from "@tabler/icons-react";
import type { Icon as TablerIcon } from "@tabler/icons-react";
import { translate } from "@/lib/i18n/locale";

export type GeneralNavItem = {
  href: string;
  label: string;
  description: string;
  icon: TablerIcon;
};

export const GENERAL_NAV_ITEMS: GeneralNavItem[] = [
  {
    href: "/settings/general/appearance",
    label: translate("Appearance"),
    description: translate("Theme, metrics, and changes panel preferences"),
    icon: IconPalette,
  },
  {
    href: "/settings/general/layouts",
    label: translate("Layouts"),
    description: translate("Task workbench layout profiles and defaults"),
    icon: IconLayoutDashboard,
  },
  {
    href: "/settings/general/terminal",
    label: translate("Terminal"),
    description: translate("Shell, terminal fonts, and link behavior"),
    icon: IconTerminal2,
  },
  {
    href: "/settings/general/notifications",
    label: translate("Notifications"),
    description: translate("Providers and notification events"),
    icon: IconBell,
  },
  {
    href: "/settings/general/editors",
    label: translate("Editors"),
    description: translate("Editor integrations and defaults"),
    icon: IconCode,
  },
  {
    href: "/settings/general/keyboard-shortcuts",
    label: translate("Keyboard Shortcuts"),
    description: translate("Chat input and command shortcuts"),
    icon: IconCommand,
  },
  {
    href: "/settings/general/task-actions",
    label: translate("Task Actions"),
    description: translate("MCP task defaults, archive safeguards, and transcript preferences"),
    icon: IconArchive,
  },
];
