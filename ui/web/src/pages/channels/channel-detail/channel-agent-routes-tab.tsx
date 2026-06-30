import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Plus, RefreshCw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { ConfirmDialog } from "@/components/shared/confirm-dialog";
import { useChannelAgentRoutes } from "../hooks/use-channel-agent-routes";
import { AgentRoutesTable } from "./channel-agent-routes-table";
import { AgentRouteDialog } from "./channel-agent-routes-dialog";
import type { AgentData } from "@/types/agent";
import type { ChannelAgentRoute } from "@/types/channel";

interface AgentRoutesTabProps {
  instanceId: string;
  agents: AgentData[];
}

// Composes the table + dialog under one tab and owns the local UI state for
// create/edit mode and confirm-delete prompt. List + mutations come from
// useChannelAgentRoutes.
export function ChannelAgentRoutesTab({ instanceId, agents }: AgentRoutesTabProps) {
  const { t } = useTranslation("channels");
  const {
    routes,
    loading,
    createRoute,
    updateRoute,
    deleteRoute,
    refresh,
  } = useChannelAgentRoutes(instanceId);

  const [editing, setEditing] = useState<ChannelAgentRoute | null>(null);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState<ChannelAgentRoute | null>(null);

  const openCreate = () => {
    setEditing(null);
    setDialogOpen(true);
  };

  const openEdit = (r: ChannelAgentRoute) => {
    setEditing(r);
    setDialogOpen(true);
  };

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between space-y-0 gap-2">
        <CardTitle className="text-base">{t("detail.agentRoutes.title")}</CardTitle>
        <div className="flex items-center gap-2">
          <Button
            variant="ghost"
            size="icon"
            className="h-7 w-7"
            onClick={() => refresh()}
            disabled={loading}
            aria-label={t("detail.agentRoutes.actions.refresh")}
          >
            <RefreshCw className={"h-3.5 w-3.5" + (loading ? " animate-spin" : "")} />
          </Button>
          <Button size="sm" onClick={openCreate}>
            <Plus className="h-3.5 w-3.5 mr-1" />
            {t("detail.agentRoutes.actions.add")}
          </Button>
        </div>
      </CardHeader>
      <CardContent>
        <p className="text-sm text-muted-foreground mb-3">
          {t("detail.agentRoutes.description")}
        </p>
        <AgentRoutesTable
          routes={routes}
          agents={agents}
          onEdit={openEdit}
          onDelete={(r) => setConfirmDelete(r)}
          onToggleEnabled={(r, v) => updateRoute(r.id, { is_enabled: v })}
          busy={loading}
        />
      </CardContent>

      <AgentRouteDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        agents={agents}
        initial={editing}
        onSubmit={async (payload) => {
          if (editing) {
            await updateRoute(editing.id, payload);
          } else {
            await createRoute(payload);
          }
        }}
      />

      <ConfirmDialog
        open={!!confirmDelete}
        onOpenChange={(open) => !open && setConfirmDelete(null)}
        title={t("detail.agentRoutes.deleteConfirm.title")}
        description={t("detail.agentRoutes.deleteConfirm.body", { name: confirmDelete?.name || confirmDelete?.id.slice(0, 8) })}
        confirmLabel={t("detail.agentRoutes.actions.delete")}
        variant="destructive"
        onConfirm={async () => {
          if (confirmDelete) {
            await deleteRoute(confirmDelete.id);
            setConfirmDelete(null);
          }
        }}
      />
    </Card>
  );
}
