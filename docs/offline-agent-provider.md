# Offline Agent Provider (`provider_type: offline`)

A rule-based provider that costs **zero LLM tokens** and needs **no API key**.
Agents configured with it become *offline agents*: the whole pipeline
(sessions, group history, routing, MCP tool execution, delivery) runs exactly
like any other agent — only the "thinking" step is deterministic
message-intent routing instead of an LLM call.

Use cases: keep core affiliate ops (shortlink + commission lookup) alive when
the LLM provider is down/unstable, or run a dedicated zero-cost bot for
link-only channels. Because it's a normal provider, `channel_agent_routes`
can mix it with LLM agents on one channel (e.g. group → offline, DM → LLM).

## Behavior

| Inbound | Offline agent responds |
|---|---|
| Platform product URL (shopee/lazada/tiktok, any context) | Calls MCP `generate_shortlink` + `get_commission_for_url` → rich reply:<br>`<tone opener>: {short_url}`<br>`📦 {product_name}`<br>`💵 Hoa hồng {rate}%` |
| URL + commission keyword ("hoa hồng", "%") | Same, and shows `💵 Hiện chưa có thông tin hoa hồng` when the lookup fails |
| Question/request addressed to bot (DM / @mention / reply-to-bot), no URL | Polite decline: "em chỉ giúp được về link affiliate với hoa hồng" |
| Group chatter (not addressed) | `NO_REPLY` → suppressed by the existing silence machinery |
| MCP shortlink call fails | Degraded apology ("hệ thống đang bận…") — never silent on a business request |

Classifier lives in `internal/msgintent/` (regex + vi/en keyword, <1ms).
Platform whitelist mirrors affiliate-backend `url_processor.py`:
`shopee.vn`, `s.shopee.vn`, `shp.ee`, `lazada.vn`, `s.lazada.vn`,
`tiktok.com`, `vt.tiktok.com`.

## How it works

`internal/providers/offline.go` implements `providers.Provider`. Two-phase
rule engine per agent-loop iteration:

1. **classify** — reads the last user message + `peer_kind`/`was_mentioned`
   from `ChatRequest.Options`; for URL intents returns `ToolCalls` whose IDs
   carry the `offline_call_` prefix. Tool names are discovered from
   `req.Tools` by suffix (`__generate_shortlink`) — no hardcoded MCP server
   prefix; the agent's own MCP grants apply as usual.
2. **compose** — when the trailing messages are tool results answering its
   own calls, parses the JSON envelopes and renders the rich reply from the
   i18n template pools (`msgintent.*` keys, en/vi/zh, 4 tones).

Channels emit the `was_mentioned` metadata signal (telegram, feishu,
zalo_personal, discord, whatsapp). On channels without it, group questions
classify as chatter → silent (safe default); DMs and URL intents are
unaffected.

## Setup

1. **Create the provider** (Dashboard → Providers → "Offline Agent (No AI)",
   no API key) or via API:

```json
POST /v1/providers
{
  "name": "offline",
  "provider_type": "offline",
  "enabled": true,
  "settings": {
    "tone": "humble",          // casual | humble | business | minimal
    "locale": "vi",            // en | vi | zh
    "reply_prefix": "",        // optional, e.g. "[offline]" — prepended to every
                               // reply (NO_REPLY suppression passes unprefixed)
    "help_text": "",           // optional value for the {help} template var;
                               // empty → built-in locale hint (msgintent.help)
    "templates": {             // optional per-slot overrides of the built-in
                               // i18n pools; remove a key to restore defaults
      "opener":            ["Link nè: {url}", "Của bạn đây: {url}"],
      "decline":           ["Em chỉ xử lý link sản phẩm thôi ạ."],
      "degraded":          ["Hệ thống đang bận, thử lại sau nhé."],
      "product_line":      ["📦 {name}"],
      "rate_line":         ["💵 Hoa hồng {rate}%"],
      "rate_missing_line": ["💵 Chưa có thông tin hoa hồng."]
    },
    "intent_config": {         // optional keyword EXTENSIONS for the classifier
      "commission_keywords": [], "broadcast_keywords": [], "question_keywords": []
    }
  }
}
```

Multiple rows = multiple presets (e.g. `offline-business` with
`{"tone":"business"}`).

**Template override semantics** (`settings.templates`):
- Keys are the 6 reply slots above; each value is a POOL — multiple entries
  give per-message variation (deterministic seed pick, same as built-ins).
- Vars per slot: `opener` → `{url}`, `product_line` → `{name}`,
  `rate_line` → `{rate}`. EVERY slot additionally gets `{help}` — a usage
  hint for guiding the user (e.g. in `decline`: `"Em không rõ ạ. {help}"`).
  `{help}` resolves to `settings.help_text`, falling back to the built-in
  locale-aware hint (`msgintent.help`, en/vi/zh).
- Absent key or empty pool → built-in i18n pool (tone × locale) — so
  "remove" = delete the key.
- An entry rendering to `""` DROPS that line — but only for the optional
  rich-block lines (`product_line`, `rate_line`, `rate_missing_line`);
  `opener`/`decline`/`degraded` are required and fall back to the built-in
  instead of going silent.
- Updating settings via `PUT /v1/providers/{id}` rebuilds the provider
  instance — no gateway restart needed.

2. **Create/point an agent at it**: agent `provider` = the row name, `model`
   = `offline` (free-text; the value is ignored). Grant the agent the
   affiliate-backend MCP server as usual — the offline provider uses the same
   grants/whitelist.

3. **Route traffic**: assign the agent to a channel instance or add
   `channel_agent_routes` rules mixing offline + LLM agents.

## Notes

- Settings parse with safe defaults (humble/vi); invalid tone falls back.
- `commission_broadcast` ("gửi bảng kê" + URL) declines in v1 — needs a
  confirm flow.
- Replies persist to session history normally, so switching the agent (or
  route) back to an LLM provider keeps full conversation context.
- Tests: `internal/providers/offline_test.go`, `internal/msgintent/*_test.go`,
  i18n completeness in `internal/i18n/msgintent_keys_completeness_test.go`.
