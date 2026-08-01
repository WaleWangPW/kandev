// Package i18n localizes the strings the backend renders directly to a browser.
//
// Scope is deliberately narrow. Almost every string the backend produces is
// diagnostic — API error payloads ("invalid payload", "wrong instance id"), log
// lines, and agent output. Those stay English: the SPA maps API failures onto its
// own translated copy, and translating a diagnostic would make logs and support
// harder without helping a user. See docs/i18n.md ("Backend").
//
// What this package covers is the surface a user reads straight from Go: the
// plain-text error pages served when the SPA bundle cannot be delivered, and any
// future server-rendered copy.
//
// Catalogs mirror the frontend's shape (`locales/<locale>.json`, flat
// `key: message`) and are embedded, so no runtime file access is needed.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

//go:embed locales/*.json
var catalogFS embed.FS

// DefaultLocale is the source locale; every other catalog falls back to it.
const DefaultLocale = "en"

// LocaleCookie is written by the SPA language switcher and is the source of
// truth for the active locale. Kept in sync with the frontend constant in
// apps/web/lib/i18n/cookie.ts.
const LocaleCookie = "kandev_locale"

// supportedLocales enumerates the locales with a committed catalog. Keep in sync
// with SUPPORTED_LOCALES in apps/web/lib/i18n/index.ts.
var supportedLocales = map[string]bool{
	"en":     true,
	"pseudo": true,
}

var (
	loadOnce sync.Once
	catalogs map[string]map[string]string
)

func load() {
	loadOnce.Do(func() {
		catalogs = make(map[string]map[string]string)
		entries, err := catalogFS.ReadDir("locales")
		if err != nil {
			return
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			data, readErr := catalogFS.ReadFile("locales/" + entry.Name())
			if readErr != nil {
				continue
			}
			messages := map[string]string{}
			if json.Unmarshal(data, &messages) != nil {
				continue
			}
			catalogs[strings.TrimSuffix(entry.Name(), ".json")] = messages
		}
	})
}

// Supported reports whether locale has a committed catalog.
func Supported(locale string) bool { return supportedLocales[locale] }

// Normalize returns locale when it is supported, otherwise DefaultLocale. Used
// for both `<html lang>` and message lookup so they can never disagree.
func Normalize(locale string) string {
	if supportedLocales[locale] {
		return locale
	}
	return DefaultLocale
}

// FromRequest resolves the active locale: the kandev_locale cookie first (the
// SPA's explicit user choice), then the browser's Accept-Language, then the
// default. Unknown values coerce to DefaultLocale rather than erroring.
func FromRequest(r *http.Request) string {
	if r == nil {
		return DefaultLocale
	}
	if cookie, err := r.Cookie(LocaleCookie); err == nil && Supported(cookie.Value) {
		return cookie.Value
	}
	for _, tag := range parseAcceptLanguage(r.Header.Get("Accept-Language")) {
		if Supported(tag) {
			return tag
		}
		// Match "en-GB" against the "en" catalog.
		if base := strings.SplitN(tag, "-", 2)[0]; Supported(base) {
			return base
		}
	}
	return DefaultLocale
}

// parseAcceptLanguage returns the header's tags in descending q-value order.
// Malformed entries are skipped rather than failing the request.
func parseAcceptLanguage(header string) []string {
	if header == "" {
		return nil
	}
	type weighted struct {
		tag string
		q   float64
	}
	var items []weighted
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(strings.TrimSpace(part), ";")
		tag := strings.ToLower(strings.TrimSpace(fields[0]))
		if tag == "" {
			continue
		}
		q := 1.0
		for _, field := range fields[1:] {
			field = strings.TrimSpace(field)
			if !strings.HasPrefix(field, "q=") {
				continue
			}
			if _, err := fmt.Sscanf(field, "q=%f", &q); err != nil {
				q = 1.0
			}
		}
		items = append(items, weighted{tag: tag, q: q})
	}
	// Stable insertion sort: preserves header order among equal q-values.
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j].q > items[j-1].q; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
	tags := make([]string, 0, len(items))
	for _, item := range items {
		tags = append(tags, item.tag)
	}
	return tags
}

// T returns the message for key in locale, falling back to DefaultLocale and
// finally to the key itself. A missing key therefore degrades to
// visible-but-wrong rather than blank — the same contract as the frontend.
func T(locale, key string) string {
	load()
	normalized := Normalize(locale)
	if messages, ok := catalogs[normalized]; ok {
		if message, found := messages[key]; found && message != "" {
			return message
		}
	}
	if normalized != DefaultLocale {
		if messages, ok := catalogs[DefaultLocale]; ok {
			if message, found := messages[key]; found && message != "" {
				return message
			}
		}
	}
	return key
}

// Keys lists every key in the source catalog; used by the drift test that keeps
// translations in step with `en`.
func Keys() []string {
	load()
	messages := catalogs[DefaultLocale]
	keys := make([]string, 0, len(messages))
	for key := range messages {
		keys = append(keys, key)
	}
	return keys
}
