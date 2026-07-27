import { useMemo, useState, useEffect } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { OfflineSettingsForm } from "@/components/shared/offline-settings-form";
import {
  parseOfflineSettings,
  serializeOfflineSettings,
  type OfflineFormState,
} from "@/lib/offline-settings-serde";
import { useProviders } from "@/pages/providers/hooks/use-providers";
import type { AgentData } from "@/types/agent";

interface Props {
  agent: AgentData;
  onUpdate: (updates: Record<string, unknown>) => Promise<void>;
}

// Read `agent.other_config.offline` back into a form-shaped state. Missing
// or malformed subkey yields defaults from `parseOfflineSettings`.
function readAgentOfflineOverride(agent: AgentData): OfflineFormState {
  const bag = (agent.other_config ?? {}) as Record<string, unknown>;
  return parseOfflineSettings(bag.offline);
}

// AgentOfflineSection lets an agent bound to an offline provider tweak
// voice/templates/keywords per-agent. When the agent points at a non-offline
// provider, the whole section is hidden (kept out of the DOM — no disabled
// state) so LLM agents stay uncluttered.
//
// Empty-override heuristic: if the serialized payload is exactly the pair
// {tone:"humble", locale:"vi"} with no other fields, we treat that as
// "revert to provider defaults" and delete the subkey. This means a user who
// deliberately picks humble+vi without changing anything else won't override
// the provider — acceptable trade-off (they can pick any other field to
// force the override, or leave provider defaults alone).
//
// Refetch during edit resets local state (matches PinnedSkillsSection); save
// frequently or expect lost WIP if the query invalidates mid-edit.
export function AgentOfflineSection({ agent, onUpdate }: Props) {
  const { t } = useTranslation("agents");
  const { providers } = useProviders();
  const currentProvider = useMemo(
    () => providers.find((p) => p.name === agent.provider),
    [providers, agent.provider],
  );
  const isOffline = currentProvider?.provider_type === "offline";

  const saved = readAgentOfflineOverride(agent);
  const [state, setState] = useState<OfflineFormState>(saved);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    setState(readAgentOfflineOverride(agent));
  }, [agent]);

  const dirty = JSON.stringify(state) !== JSON.stringify(saved);

  const handleSave = async () => {
    setSaving(true);
    try {
      const payload = serializeOfflineSettings(state);
      const bag = { ...((agent.other_config ?? {}) as Record<string, unknown>) };
      const isDefaultsOnly =
        Object.keys(payload).length === 2 &&
        payload.tone === "humble" &&
        payload.locale === "vi";
      if (isDefaultsOnly) {
        delete bag.offline;
      } else {
        bag.offline = payload;
      }
      const nextOtherConfig = Object.keys(bag).length > 0 ? bag : null;
      await onUpdate({ other_config: nextOtherConfig });
    } finally {
      setSaving(false);
    }
  };

  if (!isOffline) return null;

  return (
    <section className="space-y-3 rounded-lg border p-3 sm:p-4">
      <div className="flex items-center justify-between gap-2">
        <div className="min-w-0">
          <h3 className="text-sm font-medium">{t("offline.sectionTitle")}</h3>
          <p className="mt-0.5 text-xs text-muted-foreground">
            {t("offline.sectionDescription")}
          </p>
        </div>
        {dirty && (
          <Button size="sm" onClick={handleSave} disabled={saving}>
            {saving ? t("general.saving") : t("general.saveChanges")}
          </Button>
        )}
      </div>
      <OfflineSettingsForm value={state} onChange={setState} />
    </section>
  );
}
