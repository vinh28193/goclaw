import { useTranslation } from "react-i18next";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { TagInput } from "@/components/shared/tag-input";
import {
  OFFLINE_TEMPLATE_SLOTS,
  type OfflineFormState,
  type OfflineLocale,
  type OfflineTemplateSlot,
  type OfflineTone,
} from "@/lib/offline-settings-serde";

// Vars available per template slot — surfaced in helper text so operators
// know which placeholders they can put in each slot. Matches
// internal/providers/offline_settings.go slot semantics.
const SLOT_VARS: Record<OfflineTemplateSlot, string> = {
  opener: "{url}, {help}",
  decline: "{help}",
  degraded: "{help}",
  productLine: "{name}, {help}",
  rateLine: "{rate}, {help}",
  rateMissingLine: "{help}",
};

const TONE_OPTIONS: OfflineTone[] = ["humble", "casual", "business", "minimal"];
const LOCALE_OPTIONS: OfflineLocale[] = ["vi", "en", "zh"];

interface OfflineSettingsFormProps {
  value: OfflineFormState;
  onChange: (next: OfflineFormState) => void;
}

export function OfflineSettingsForm({
  value,
  onChange,
}: OfflineSettingsFormProps) {
  const { t } = useTranslation("providers");

  // Single-key updater avoids repeating the spread; keeps prop calls thin.
  const update = <K extends keyof OfflineFormState>(
    key: K,
    v: OfflineFormState[K],
  ) => onChange({ ...value, [key]: v });

  const updateTemplate = (slot: OfflineTemplateSlot, v: string) =>
    onChange({ ...value, templates: { ...value.templates, [slot]: v } });

  return (
    <div className="space-y-3">
      <p className="text-sm text-muted-foreground">{t("offline.description")}</p>

      <details open className="rounded-md border p-3">
        <summary className="cursor-pointer text-sm font-medium">
          {t("offline.groups.voice")}
        </summary>
        <div className="mt-3 grid grid-cols-1 gap-4 sm:grid-cols-2">
          <div className="space-y-2">
            <Label>{t("offline.tone.label")}</Label>
            <Select
              value={value.tone}
              onValueChange={(v) => update("tone", v as OfflineTone)}
            >
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {TONE_OPTIONS.map((tone) => (
                  <SelectItem key={tone} value={tone}>
                    {t(`offline.tone.${tone}`)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-2">
            <Label>{t("offline.locale.label")}</Label>
            <Select
              value={value.locale}
              onValueChange={(v) => update("locale", v as OfflineLocale)}
            >
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {LOCALE_OPTIONS.map((loc) => (
                  <SelectItem key={loc} value={loc}>
                    {t(`offline.locale.${loc}`)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </div>
      </details>

      <details className="rounded-md border p-3">
        <summary className="cursor-pointer text-sm font-medium">
          {t("offline.groups.prefixHelp")}
        </summary>
        <div className="mt-3 space-y-3">
          <div className="space-y-2">
            <Label htmlFor="offlineReplyPrefix">
              {t("offline.replyPrefix.label")}
            </Label>
            <Input
              id="offlineReplyPrefix"
              value={value.replyPrefix}
              onChange={(e) => update("replyPrefix", e.target.value)}
              placeholder={t("offline.replyPrefix.placeholder")}
            />
            <p className="text-xs text-muted-foreground">
              {t("offline.replyPrefix.hint")}
            </p>
          </div>
          <div className="space-y-2">
            <Label htmlFor="offlineHelpText">
              {t("offline.helpText.label")}
            </Label>
            <Textarea
              id="offlineHelpText"
              size="sm"
              value={value.helpText}
              onChange={(e) => update("helpText", e.target.value)}
              placeholder={t("offline.helpText.placeholder")}
            />
            <p className="text-xs text-muted-foreground">
              {t("offline.helpText.hint")}
            </p>
          </div>
        </div>
      </details>

      <details className="rounded-md border p-3">
        <summary className="cursor-pointer text-sm font-medium">
          {t("offline.groups.templates")}
        </summary>
        <div className="mt-3 space-y-4">
          <p className="text-xs text-muted-foreground">
            {t("offline.templates.commonHint")}
          </p>
          {OFFLINE_TEMPLATE_SLOTS.map((slot) => (
            <div key={slot} className="space-y-2">
              <Label htmlFor={`offlineTpl_${slot}`}>
                {t(`offline.templates.${slot}.label`)}
              </Label>
              <Textarea
                id={`offlineTpl_${slot}`}
                value={value.templates[slot]}
                onChange={(e) => updateTemplate(slot, e.target.value)}
                placeholder={t(`offline.templates.${slot}.placeholder`)}
              />
              <p className="text-xs text-muted-foreground">
                {t("offline.templates.vars", { vars: SLOT_VARS[slot] })}
              </p>
            </div>
          ))}
        </div>
      </details>

      <details className="rounded-md border p-3">
        <summary className="cursor-pointer text-sm font-medium">
          {t("offline.groups.keywords")}
        </summary>
        <div className="mt-3 space-y-4">
          <div className="space-y-2">
            <Label>{t("offline.keywords.commission.label")}</Label>
            <TagInput
              value={value.commissionKeywords}
              onChange={(v) => update("commissionKeywords", v)}
            />
            <p className="text-xs text-muted-foreground">
              {t("offline.keywords.commission.hint")}
            </p>
          </div>
          <div className="space-y-2">
            <Label>{t("offline.keywords.broadcast.label")}</Label>
            <TagInput
              value={value.broadcastKeywords}
              onChange={(v) => update("broadcastKeywords", v)}
            />
            <p className="text-xs text-muted-foreground">
              {t("offline.keywords.broadcast.hint")}
            </p>
          </div>
          <div className="space-y-2">
            <Label>{t("offline.keywords.question.label")}</Label>
            <TagInput
              value={value.questionKeywords}
              onChange={(v) => update("questionKeywords", v)}
            />
            <p className="text-xs text-muted-foreground">
              {t("offline.keywords.question.hint")}
            </p>
          </div>
        </div>
      </details>
    </div>
  );
}
