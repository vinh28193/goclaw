// RespondFilterFields — UI for per-instance respond_filter config.
// Field names MUST match Go JSON tags: mode, url_domains, keywords,
// classifier_model, classifier_prompt, on_no_match, apply_scope.

import { useTranslation } from "react-i18next";
import { Label } from "@/components/ui/label";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import type { RespondFilter, RespondFilterMode, ApplyScope, OnNoMatch } from "./channel-schemas";

interface RespondFilterFieldsProps {
  value: RespondFilter | undefined;
  onChange: (next: RespondFilter | undefined) => void;
}

function arrFromText(text: string): string[] {
  return text
    .split("\n")
    .map((s) => s.trim())
    .filter(Boolean);
}

function arrToText(arr: string[] | undefined): string {
  return (arr ?? []).join("\n");
}

/** Emit clean RespondFilter or undefined (when mode=off). Drops empty strings/arrays. */
function buildFilter(
  mode: RespondFilterMode,
  domains: string,
  keywords: string,
  classifierModel: string,
  classifierPrompt: string,
  onNoMatch: OnNoMatch,
  applyScope: ApplyScope,
): RespondFilter | undefined {
  if (mode === "off") return undefined;
  const filter: RespondFilter = { mode };
  const domArr = arrFromText(domains);
  const kwArr = arrFromText(keywords);
  if (domArr.length > 0) filter.url_domains = domArr;
  if (kwArr.length > 0) filter.keywords = kwArr;
  if (classifierModel.trim()) filter.classifier_model = classifierModel.trim();
  if (classifierPrompt.trim()) filter.classifier_prompt = classifierPrompt.trim();
  if (onNoMatch !== "ignore") filter.on_no_match = onNoMatch;
  if (applyScope !== "both") filter.apply_scope = applyScope;
  return filter;
}

export function RespondFilterFields({ value, onChange }: RespondFilterFieldsProps) {
  const { t } = useTranslation("channels");

  const mode: RespondFilterMode = value?.mode ?? "off";
  const applyScope: ApplyScope = value?.apply_scope ?? "both";
  const onNoMatch: OnNoMatch = value?.on_no_match ?? "ignore";
  const domains = arrToText(value?.url_domains);
  const keywords = arrToText(value?.keywords);
  const classifierModel = value?.classifier_model ?? "";
  const classifierPrompt = value?.classifier_prompt ?? "";

  const isActive = mode !== "off";
  const showRegexFields = mode === "regex" || mode === "hybrid";
  const showClassifierFields = mode === "classifier" || mode === "hybrid";

  const emit = (
    newMode: RespondFilterMode,
    newDomains: string,
    newKeywords: string,
    newClassifierModel: string,
    newClassifierPrompt: string,
    newOnNoMatch: OnNoMatch,
    newApplyScope: ApplyScope,
  ) => {
    onChange(
      buildFilter(newMode, newDomains, newKeywords, newClassifierModel, newClassifierPrompt, newOnNoMatch, newApplyScope),
    );
  };

  return (
    <fieldset className="rounded-md border p-3 space-y-3">
      <legend className="px-1 text-sm font-medium">{t("respondFilter.title")}</legend>

      <p className="text-xs text-muted-foreground">{t("respondFilter.help")}</p>

      {/* Mode select — always visible */}
      <div className="grid gap-1.5">
        <Label className="text-sm">{t("respondFilter.mode")}</Label>
        <Select
          value={mode}
          onValueChange={(v) =>
            emit(v as RespondFilterMode, domains, keywords, classifierModel, classifierPrompt, onNoMatch, applyScope)
          }
        >
          <SelectTrigger className="text-base md:text-sm">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="off">{t("respondFilter.modes.off")}</SelectItem>
            <SelectItem value="regex">{t("respondFilter.modes.regex")}</SelectItem>
            <SelectItem value="classifier">{t("respondFilter.modes.classifier")}</SelectItem>
            <SelectItem value="hybrid">{t("respondFilter.modes.hybrid")}</SelectItem>
          </SelectContent>
        </Select>
      </div>

      {/* Remaining fields only when mode != off */}
      {isActive && (
        <>
          {/* Apply scope */}
          <div className="grid gap-1.5">
            <Label className="text-sm">{t("respondFilter.applyScope")}</Label>
            <Select
              value={applyScope}
              onValueChange={(v) =>
                emit(mode, domains, keywords, classifierModel, classifierPrompt, onNoMatch, v as ApplyScope)
              }
            >
              <SelectTrigger className="text-base md:text-sm">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="both">{t("respondFilter.applyScopes.both")}</SelectItem>
                <SelectItem value="direct">{t("respondFilter.applyScopes.direct")}</SelectItem>
                <SelectItem value="group">{t("respondFilter.applyScopes.group")}</SelectItem>
              </SelectContent>
            </Select>
          </div>

          {/* On no match */}
          <div className="grid gap-1.5">
            <Label className="text-sm">{t("respondFilter.onNoMatch")}</Label>
            <Select
              value={onNoMatch}
              onValueChange={(v) =>
                emit(mode, domains, keywords, classifierModel, classifierPrompt, v as OnNoMatch, applyScope)
              }
            >
              <SelectTrigger className="text-base md:text-sm">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="ignore">{t("respondFilter.onNoMatchValues.ignore")}</SelectItem>
                <SelectItem value="wake">{t("respondFilter.onNoMatchValues.wake")}</SelectItem>
              </SelectContent>
            </Select>
            <p className="text-xs text-muted-foreground">{t("respondFilter.onNoMatchHint")}</p>
          </div>

          {/* URL domains — regex / hybrid only */}
          {showRegexFields && (
            <div className="grid gap-1.5">
              <Label className="text-sm">{t("respondFilter.urlDomains")}</Label>
              <Textarea
                className="text-base md:text-sm min-h-[80px] font-mono text-xs"
                placeholder={t("respondFilter.urlDomainsHint")}
                value={domains}
                onChange={(e) =>
                  emit(mode, e.target.value, keywords, classifierModel, classifierPrompt, onNoMatch, applyScope)
                }
              />
              <p className="text-xs text-muted-foreground">{t("respondFilter.urlDomainsHint")}</p>
            </div>
          )}

          {/* Keywords — regex / hybrid only */}
          {showRegexFields && (
            <div className="grid gap-1.5">
              <Label className="text-sm">{t("respondFilter.keywords")}</Label>
              <Textarea
                className="text-base md:text-sm min-h-[80px] font-mono text-xs"
                placeholder={t("respondFilter.keywordsHint")}
                value={keywords}
                onChange={(e) =>
                  emit(mode, domains, e.target.value, classifierModel, classifierPrompt, onNoMatch, applyScope)
                }
              />
              <p className="text-xs text-muted-foreground">{t("respondFilter.keywordsHint")}</p>
            </div>
          )}

          {/* Classifier model — classifier / hybrid */}
          {showClassifierFields && (
            <div className="grid gap-1.5">
              <Label className="text-sm">{t("respondFilter.classifierModel")}</Label>
              <Input
                className="text-base md:text-sm"
                placeholder={t("respondFilter.classifierModelHint")}
                value={classifierModel}
                onChange={(e) =>
                  emit(mode, domains, keywords, e.target.value, classifierPrompt, onNoMatch, applyScope)
                }
              />
              <p className="text-xs text-muted-foreground">{t("respondFilter.classifierModelHint")}</p>
            </div>
          )}

          {/* Classifier prompt — classifier / hybrid */}
          {showClassifierFields && (
            <div className="grid gap-1.5">
              <Label className="text-sm">{t("respondFilter.classifierPrompt")}</Label>
              <Textarea
                className="text-base md:text-sm min-h-[100px]"
                placeholder={t("respondFilter.classifierPromptHint")}
                value={classifierPrompt}
                onChange={(e) =>
                  emit(mode, domains, keywords, classifierModel, e.target.value, onNoMatch, applyScope)
                }
              />
              <p className="text-xs text-muted-foreground">{t("respondFilter.classifierPromptHint")}</p>
            </div>
          )}
        </>
      )}
    </fieldset>
  );
}
