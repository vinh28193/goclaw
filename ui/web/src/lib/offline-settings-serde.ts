// Serialize/parse helpers between OfflineFormState (UI-friendly) and the
// backend JSONB shape stored in llm_providers.settings for provider_type
// "offline". Backend contract lives in
// internal/providers/offline_settings.go (OfflineSettings + Templates map +
// IntentConfig). "Omit when default" semantics are enforced here so the UI
// never sends empty strings/arrays that would override backend defaults.

export type OfflineTone = "humble" | "casual" | "business" | "minimal";
export type OfflineLocale = "en" | "vi" | "zh";

// Slot keys mirror internal/providers/offline_settings.go Slot* consts. UI
// state uses camelCase; wire format keeps backend snake_case.
export const OFFLINE_TEMPLATE_SLOTS = [
  "opener",
  "decline",
  "degraded",
  "productLine",
  "rateLine",
  "rateMissingLine",
] as const;
export type OfflineTemplateSlot = (typeof OFFLINE_TEMPLATE_SLOTS)[number];

// Slots where the entire pool falling back is desirable when the operator
// clears it (opener/decline/degraded must NEVER go silent — backend renders
// built-in i18n instead). Empty *lines* inside these slots are meaningless
// and dropped.
const REQUIRED_SLOTS: ReadonlySet<OfflineTemplateSlot> = new Set([
  "opener",
  "decline",
  "degraded",
]);

// Slot ↔ wire-format key mapping. Kept explicit (rather than snake_case()
// helper) so refactors surface as type errors.
const SLOT_WIRE_KEYS: Record<OfflineTemplateSlot, string> = {
  opener: "opener",
  decline: "decline",
  degraded: "degraded",
  productLine: "product_line",
  rateLine: "rate_line",
  rateMissingLine: "rate_missing_line",
};

export interface OfflineFormState {
  tone: OfflineTone;
  locale: OfflineLocale;
  replyPrefix: string;
  helpText: string;
  templates: Record<OfflineTemplateSlot, string>;
  commissionKeywords: string[];
  broadcastKeywords: string[];
  questionKeywords: string[];
}

export interface OfflineSettingsPayload {
  tone?: string;
  locale?: string;
  reply_prefix?: string;
  help_text?: string;
  templates?: Record<string, string[]>;
  intent_config?: {
    commission_keywords?: string[];
    broadcast_keywords?: string[];
    question_keywords?: string[];
  };
}

const VALID_TONES: ReadonlySet<string> = new Set([
  "humble",
  "casual",
  "business",
  "minimal",
]);
const VALID_LOCALES: ReadonlySet<string> = new Set(["en", "vi", "zh"]);

export function emptyOfflineFormState(): OfflineFormState {
  return {
    tone: "humble",
    locale: "vi",
    replyPrefix: "",
    helpText: "",
    templates: {
      opener: "",
      decline: "",
      degraded: "",
      productLine: "",
      rateLine: "",
      rateMissingLine: "",
    },
    commissionKeywords: [],
    broadcastKeywords: [],
    questionKeywords: [],
  };
}

// parseTemplateLines / serializeTemplateLines are the low-level converters
// between the textarea string and the wire `string[]`. Two modes:
//   keepEmpty=false (required slots) — filter blank lines
//   keepEmpty=true  (drop-line slots) — preserve blank entries (they mean
//                                        "drop the line" at render time),
//                                        but strip a single trailing blank
//                                        that the browser adds when the user
//                                        ends the text with newline.
export function parseTemplateLines(text: string, keepEmpty: boolean): string[] {
  if (text === "") return [];
  const lines = text.split("\n");
  if (!keepEmpty) {
    return lines.filter((line) => line.trim() !== "");
  }
  if (lines.length > 0 && lines[lines.length - 1] === "") {
    lines.pop();
  }
  return lines;
}

export function serializeTemplateLines(lines: string[]): string {
  return lines.join("\n");
}

function keepEmptyForSlot(slot: OfflineTemplateSlot): boolean {
  return !REQUIRED_SLOTS.has(slot);
}

// parseOfflineSettings accepts arbitrary JSON (typed as `unknown`) — malformed
// input yields a defaulted form state rather than throwing. Mirrors the Go
// zero-value fallback in ParseOfflineSettings.
export function parseOfflineSettings(raw: unknown): OfflineFormState {
  const state = emptyOfflineFormState();
  if (!raw || typeof raw !== "object") return state;
  const p = raw as OfflineSettingsPayload;

  if (typeof p.tone === "string" && VALID_TONES.has(p.tone)) {
    state.tone = p.tone as OfflineTone;
  }
  if (typeof p.locale === "string" && VALID_LOCALES.has(p.locale)) {
    state.locale = p.locale as OfflineLocale;
  }
  if (typeof p.reply_prefix === "string") state.replyPrefix = p.reply_prefix;
  if (typeof p.help_text === "string") state.helpText = p.help_text;

  if (p.templates && typeof p.templates === "object") {
    for (const slot of OFFLINE_TEMPLATE_SLOTS) {
      const wire = SLOT_WIRE_KEYS[slot];
      const pool = p.templates[wire];
      if (Array.isArray(pool)) {
        state.templates[slot] = serializeTemplateLines(
          pool.filter((v): v is string => typeof v === "string"),
        );
      }
    }
  }

  const ic = p.intent_config;
  if (ic && typeof ic === "object") {
    state.commissionKeywords = normalizeStringArray(ic.commission_keywords);
    state.broadcastKeywords = normalizeStringArray(ic.broadcast_keywords);
    state.questionKeywords = normalizeStringArray(ic.question_keywords);
  }

  return state;
}

function normalizeStringArray(v: unknown): string[] {
  if (!Array.isArray(v)) return [];
  return v.filter((s): s is string => typeof s === "string");
}

// serializeOfflineSettings builds the JSONB payload with strict OMIT rules:
// every absent field means "use backend default", so we never send empty
// strings/arrays that would look like an explicit override.
export function serializeOfflineSettings(
  state: OfflineFormState,
): OfflineSettingsPayload {
  const out: OfflineSettingsPayload = {};

  // Tone/locale always sent — the UI select always has a concrete value and
  // sending it makes the stored settings self-describing.
  out.tone = state.tone;
  out.locale = state.locale;

  if (state.replyPrefix !== "") out.reply_prefix = state.replyPrefix;
  if (state.helpText !== "") out.help_text = state.helpText;

  const templates: Record<string, string[]> = {};
  for (const slot of OFFLINE_TEMPLATE_SLOTS) {
    const lines = parseTemplateLines(
      state.templates[slot],
      keepEmptyForSlot(slot),
    );
    if (lines.length > 0) {
      templates[SLOT_WIRE_KEYS[slot]] = lines;
    }
  }
  if (Object.keys(templates).length > 0) {
    out.templates = templates;
  }

  const intent: NonNullable<OfflineSettingsPayload["intent_config"]> = {};
  if (state.commissionKeywords.length > 0) {
    intent.commission_keywords = state.commissionKeywords;
  }
  if (state.broadcastKeywords.length > 0) {
    intent.broadcast_keywords = state.broadcastKeywords;
  }
  if (state.questionKeywords.length > 0) {
    intent.question_keywords = state.questionKeywords;
  }
  if (Object.keys(intent).length > 0) {
    out.intent_config = intent;
  }

  return out;
}
