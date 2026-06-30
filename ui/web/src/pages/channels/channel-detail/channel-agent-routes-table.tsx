import { useTranslation } from "react-i18next";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { Pencil, Trash2 } from "lucide-react";
import type { ChannelAgentRoute } from "@/types/channel";
import type { AgentData } from "@/types/agent";

interface AgentRoutesTableProps {
  routes: ChannelAgentRoute[];
  agents: AgentData[];
  onEdit: (route: ChannelAgentRoute) => void;
  onDelete: (route: ChannelAgentRoute) => void;
  onToggleEnabled: (route: ChannelAgentRoute, enabled: boolean) => void;
  busy?: boolean;
}

// Renders the per-channel routing table. Mobile-friendly: wraps in
// overflow-x-auto and sets min-w-[760px] on the inner table so columns don't
// crush on narrow screens (per CLAUDE.md mobile UI rules).
export function AgentRoutesTable({
  routes,
  agents,
  onEdit,
  onDelete,
  onToggleEnabled,
  busy,
}: AgentRoutesTableProps) {
  const { t } = useTranslation("channels");

  if (routes.length === 0) {
    return (
      <div className="rounded-lg border border-dashed border-muted-foreground/20 p-6 text-center text-sm text-muted-foreground">
        {t("detail.agentRoutes.empty")}
      </div>
    );
  }

  const agentLabel = (id: string) => {
    const a = agents.find((x) => x.id === id);
    return a?.display_name || a?.agent_key || id.slice(0, 8);
  };

  return (
    <div className="overflow-x-auto">
      <table className="min-w-[760px] w-full text-sm">
        <thead className="text-muted-foreground">
          <tr className="border-b">
            <th className="px-2 py-2 text-left font-medium">{t("detail.agentRoutes.columns.name")}</th>
            <th className="px-2 py-2 text-left font-medium">{t("detail.agentRoutes.columns.agent")}</th>
            <th className="px-2 py-2 text-left font-medium">{t("detail.agentRoutes.columns.peerKind")}</th>
            <th className="px-2 py-2 text-left font-medium">{t("detail.agentRoutes.columns.mediaType")}</th>
            <th className="px-2 py-2 text-left font-medium">{t("detail.agentRoutes.columns.mention")}</th>
            <th className="px-2 py-2 text-right font-medium">{t("detail.agentRoutes.columns.priority")}</th>
            <th className="px-2 py-2 text-left font-medium">{t("detail.agentRoutes.columns.toolAllow")}</th>
            <th className="px-2 py-2 text-center font-medium">{t("detail.agentRoutes.columns.enabled")}</th>
            <th className="px-2 py-2 text-right font-medium">{t("detail.agentRoutes.columns.actions")}</th>
          </tr>
        </thead>
        <tbody>
          {routes.map((r) => (
            <tr key={r.id} className="border-b last:border-b-0">
              <td className="px-2 py-2">{r.name || <span className="text-muted-foreground">—</span>}</td>
              <td className="px-2 py-2">{agentLabel(r.agent_id)}</td>
              <td className="px-2 py-2">
                <Badge variant="secondary">{t(`detail.agentRoutes.peerKind.${r.peer_kind}`)}</Badge>
              </td>
              <td className="px-2 py-2">
                <Badge variant="outline">
                  {r.media_type
                    ? t(`detail.agentRoutes.mediaType.${r.media_type}`)
                    : t("detail.agentRoutes.mediaType.any")}
                </Badge>
              </td>
              <td className="px-2 py-2">{r.mention_required ? t("detail.agentRoutes.mentionYes") : "—"}</td>
              <td className="px-2 py-2 text-right tabular-nums">{r.priority}</td>
              <td className="px-2 py-2">
                {r.tool_allow === null ? (
                  <Badge variant="outline">{t("detail.agentRoutes.toolAllow.inherit")}</Badge>
                ) : (
                  <Badge variant="secondary" title={r.tool_allow.join(", ")}>
                    {t("detail.agentRoutes.toolAllow.count", { count: r.tool_allow.length })}
                  </Badge>
                )}
              </td>
              <td className="px-2 py-2 text-center">
                <Switch
                  checked={r.is_enabled}
                  onCheckedChange={(v) => onToggleEnabled(r, v)}
                  disabled={busy}
                />
              </td>
              <td className="px-2 py-2 text-right">
                <Button variant="ghost" size="icon" className="h-7 w-7" onClick={() => onEdit(r)} aria-label={t("detail.agentRoutes.actions.edit")}>
                  <Pencil className="h-3.5 w-3.5" />
                </Button>
                <Button variant="ghost" size="icon" className="h-7 w-7 text-destructive" onClick={() => onDelete(r)} aria-label={t("detail.agentRoutes.actions.delete")}>
                  <Trash2 className="h-3.5 w-3.5" />
                </Button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
