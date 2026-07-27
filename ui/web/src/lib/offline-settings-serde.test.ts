import { describe, expect, it } from "vitest";
import {
  emptyOfflineFormState,
  parseOfflineSettings,
  parseTemplateLines,
  serializeOfflineSettings,
  serializeTemplateLines,
  type OfflineFormState,
} from "./offline-settings-serde";

describe("parseTemplateLines", () => {
  it("returns empty array for empty string", () => {
    expect(parseTemplateLines("", false)).toEqual([]);
    expect(parseTemplateLines("", true)).toEqual([]);
  });

  it("filters blank lines when keepEmpty=false", () => {
    expect(parseTemplateLines("a\n\nb\n  \nc", false)).toEqual(["a", "b", "c"]);
  });

  it("keeps blank interior lines when keepEmpty=true", () => {
    expect(parseTemplateLines("a\n\nb", true)).toEqual(["a", "", "b"]);
  });

  it("strips exactly one trailing empty line when keepEmpty=true", () => {
    expect(parseTemplateLines("a\nb\n", true)).toEqual(["a", "b"]);
    expect(parseTemplateLines("a\nb\n\n", true)).toEqual(["a", "b", ""]);
  });

  it("preserves leading spaces (templates may need indentation prefix)", () => {
    expect(parseTemplateLines("  hello\n\tworld", false)).toEqual([
      "  hello",
      "\tworld",
    ]);
  });
});

describe("serializeTemplateLines", () => {
  it("joins with newlines", () => {
    expect(serializeTemplateLines(["a", "b", "c"])).toBe("a\nb\nc");
  });

  it("preserves empty entries as blank lines", () => {
    expect(serializeTemplateLines(["a", "", "b"])).toBe("a\n\nb");
  });
});

describe("parseOfflineSettings", () => {
  it("returns defaults for null/undefined/non-object", () => {
    const d = emptyOfflineFormState();
    expect(parseOfflineSettings(null)).toEqual(d);
    expect(parseOfflineSettings(undefined)).toEqual(d);
    expect(parseOfflineSettings("not an object")).toEqual(d);
    expect(parseOfflineSettings(42)).toEqual(d);
  });

  it("falls back to humble+vi when tone/locale invalid", () => {
    const s = parseOfflineSettings({ tone: "grumpy", locale: "es" });
    expect(s.tone).toBe("humble");
    expect(s.locale).toBe("vi");
  });

  it("accepts valid tone/locale", () => {
    const s = parseOfflineSettings({ tone: "casual", locale: "en" });
    expect(s.tone).toBe("casual");
    expect(s.locale).toBe("en");
  });

  it("wire snake_case → UI camelCase for templates", () => {
    const s = parseOfflineSettings({
      templates: {
        opener: ["hi {url}", "hello {url}"],
        product_line: ["📦 {name}"],
        rate_missing_line: [""],
      },
    });
    expect(s.templates.opener).toBe("hi {url}\nhello {url}");
    expect(s.templates.productLine).toBe("📦 {name}");
    expect(s.templates.rateMissingLine).toBe("");
  });

  it("intent_config → per-list keywords, missing = empty", () => {
    const s = parseOfflineSettings({
      intent_config: {
        commission_keywords: ["hoa hồng", "%"],
      },
    });
    expect(s.commissionKeywords).toEqual(["hoa hồng", "%"]);
    expect(s.broadcastKeywords).toEqual([]);
    expect(s.questionKeywords).toEqual([]);
  });

  it("drops non-string entries defensively", () => {
    const s = parseOfflineSettings({
      templates: { opener: ["ok", 42, null, "still ok"] },
      intent_config: { commission_keywords: ["k", 7] },
    });
    expect(s.templates.opener).toBe("ok\nstill ok");
    expect(s.commissionKeywords).toEqual(["k"]);
  });
});

describe("serializeOfflineSettings", () => {
  it("empty state → tone+locale only", () => {
    const out = serializeOfflineSettings(emptyOfflineFormState());
    expect(out).toEqual({ tone: "humble", locale: "vi" });
  });

  it("omits reply_prefix / help_text when blank", () => {
    const s = emptyOfflineFormState();
    const out = serializeOfflineSettings(s);
    expect(out).not.toHaveProperty("reply_prefix");
    expect(out).not.toHaveProperty("help_text");
  });

  it("includes reply_prefix / help_text when non-blank", () => {
    const s: OfflineFormState = {
      ...emptyOfflineFormState(),
      replyPrefix: "[offline]",
      helpText: "help me",
    };
    const out = serializeOfflineSettings(s);
    expect(out.reply_prefix).toBe("[offline]");
    expect(out.help_text).toBe("help me");
  });

  it("filters blank lines for required slots (opener/decline/degraded)", () => {
    const s: OfflineFormState = {
      ...emptyOfflineFormState(),
      templates: {
        opener: "a\n\nb",
        decline: "  ",
        degraded: "",
        productLine: "",
        rateLine: "",
        rateMissingLine: "",
      },
    };
    const out = serializeOfflineSettings(s);
    expect(out.templates?.opener).toEqual(["a", "b"]);
    expect(out.templates?.decline).toBeUndefined();
    expect(out.templates?.degraded).toBeUndefined();
  });

  it("keeps blank lines for drop-line slots (product/rate/rate_missing)", () => {
    const s: OfflineFormState = {
      ...emptyOfflineFormState(),
      templates: {
        opener: "",
        decline: "",
        degraded: "",
        productLine: "📦 {name}\n",
        rateLine: "line1\n\nline3",
        rateMissingLine: "",
      },
    };
    const out = serializeOfflineSettings(s);
    // trailing newline stripped, no interior blanks in input
    expect(out.templates?.product_line).toEqual(["📦 {name}"]);
    // interior blank preserved
    expect(out.templates?.rate_line).toEqual(["line1", "", "line3"]);
    // empty stays absent
    expect(out.templates?.rate_missing_line).toBeUndefined();
  });

  it("omits templates entirely when all slots empty", () => {
    const out = serializeOfflineSettings(emptyOfflineFormState());
    expect(out).not.toHaveProperty("templates");
  });

  it("omits intent_config when all lists empty", () => {
    const out = serializeOfflineSettings(emptyOfflineFormState());
    expect(out).not.toHaveProperty("intent_config");
  });

  it("only sends intent_config lists that have entries", () => {
    const s: OfflineFormState = {
      ...emptyOfflineFormState(),
      commissionKeywords: ["hoa hồng"],
      broadcastKeywords: [],
      questionKeywords: ["?"],
    };
    const out = serializeOfflineSettings(s);
    expect(out.intent_config?.commission_keywords).toEqual(["hoa hồng"]);
    expect(out.intent_config?.broadcast_keywords).toBeUndefined();
    expect(out.intent_config?.question_keywords).toEqual(["?"]);
  });

  it("uses wire snake_case for template slot keys", () => {
    const s: OfflineFormState = {
      ...emptyOfflineFormState(),
      templates: {
        opener: "hi",
        decline: "",
        degraded: "",
        productLine: "p",
        rateLine: "r",
        rateMissingLine: "m",
      },
    };
    const out = serializeOfflineSettings(s);
    expect(Object.keys(out.templates ?? {}).sort()).toEqual([
      "opener",
      "product_line",
      "rate_line",
      "rate_missing_line",
    ]);
  });
});

describe("round-trip parse → serialize", () => {
  it("preserves a full payload", () => {
    const payload = {
      tone: "casual",
      locale: "en",
      reply_prefix: "[bot]",
      help_text: "usage hint",
      templates: {
        opener: ["a", "b"],
        product_line: ["📦 {name}"],
        rate_line: ["", "💵 {rate}"],
      },
      intent_config: {
        commission_keywords: ["ck"],
        question_keywords: ["?"],
      },
    };
    const state = parseOfflineSettings(payload);
    const out = serializeOfflineSettings(state);
    expect(out).toEqual(payload);
  });

  it("normalizes missing tone/locale into defaults on round-trip", () => {
    const state = parseOfflineSettings({});
    const out = serializeOfflineSettings(state);
    expect(out).toEqual({ tone: "humble", locale: "vi" });
  });
});
