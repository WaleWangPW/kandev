"use client";

import { useEffect } from "react";
import { translateRenderedDom, useI18n } from "@/lib/i18n/locale";

/** Keeps the document language aligned with the display locale for assistive tools. */
export function LocaleDocumentBridge() {
  const { locale } = useI18n();

  useEffect(() => {
    document.documentElement.lang = locale;

    // A number of low-frequency settings and integration panels still render
    // literal labels. Keep those legacy panels covered while they migrate to
    // useI18n directly; user-authored/task-content regions are excluded by
    // translateRenderedDom.
    if (!document.body) return;
    let scheduled = false;
    const apply = () => {
      scheduled = false;
      translateRenderedDom(document.body, locale);
    };
    const schedule = () => {
      if (scheduled) return;
      scheduled = true;
      queueMicrotask(apply);
    };
    apply();
    const observer = new MutationObserver(schedule);
    observer.observe(document.body, {
      childList: true,
      subtree: true,
      characterData: true,
      attributes: true,
      attributeFilter: ["aria-label", "title", "placeholder", "data-placeholder"],
    });
    return () => observer.disconnect();
  }, [locale]);

  return null;
}
