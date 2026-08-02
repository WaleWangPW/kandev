"use client";

import { useEffect } from "react";
import { useI18n } from "@/lib/i18n/locale";

/** Keeps the document language aligned with the display locale for assistive tools. */
export function LocaleDocumentBridge() {
  const { locale } = useI18n();

  useEffect(() => {
    document.documentElement.lang = locale;
  }, [locale]);

  return null;
}
