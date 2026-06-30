import { useCallback } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useHttp } from "@/hooks/use-ws";
import { queryKeys } from "@/lib/query-keys";
import { toast } from "@/stores/use-toast-store";
import i18next from "i18next";
import { userFriendlyError } from "@/lib/error-utils";
import type {
  ChannelAgentRoute,
  ChannelAgentRouteInput,
} from "@/types/channel";

// Per-channel agent-route CRUD via /v1/channels/instances/{id}/agent-routes.
// Backend mutates resolver cache server-side; the hook just invalidates the
// list query and lets React Query refetch.
export function useChannelAgentRoutes(instanceId: string | undefined) {
  const http = useHttp();
  const queryClient = useQueryClient();

  const listKey = queryKeys.channels.agentRoutes(instanceId ?? "");

  const { data, isLoading: loading, error } = useQuery({
    queryKey: listKey,
    queryFn: async () => {
      const res = await http.get<{ routes: ChannelAgentRoute[]; total: number }>(
        `/v1/channels/instances/${instanceId}/agent-routes`,
      );
      return res.routes ?? [];
    },
    enabled: !!instanceId,
    staleTime: 30_000,
  });

  const invalidate = useCallback(() => {
    return queryClient.invalidateQueries({ queryKey: listKey });
  }, [queryClient, listKey]);

  const createRoute = useCallback(
    async (input: ChannelAgentRouteInput) => {
      if (!instanceId) return null;
      try {
        const created = await http.post<ChannelAgentRoute>(
          `/v1/channels/instances/${instanceId}/agent-routes`,
          input,
        );
        await invalidate();
        toast.success(i18next.t("channels:detail.agentRoutes.toast.created"));
        return created;
      } catch (err) {
        toast.error(
          i18next.t("channels:detail.agentRoutes.toast.failedCreate"),
          userFriendlyError(err),
        );
        throw err;
      }
    },
    [instanceId, http, invalidate],
  );

  const updateRoute = useCallback(
    async (routeId: string, patch: Partial<ChannelAgentRouteInput>) => {
      if (!instanceId) return null;
      try {
        const updated = await http.patch<ChannelAgentRoute>(
          `/v1/channels/instances/${instanceId}/agent-routes/${routeId}`,
          patch,
        );
        await invalidate();
        toast.success(i18next.t("channels:detail.agentRoutes.toast.updated"));
        return updated;
      } catch (err) {
        toast.error(
          i18next.t("channels:detail.agentRoutes.toast.failedUpdate"),
          userFriendlyError(err),
        );
        throw err;
      }
    },
    [instanceId, http, invalidate],
  );

  const deleteRoute = useCallback(
    async (routeId: string) => {
      if (!instanceId) return;
      try {
        await http.delete(
          `/v1/channels/instances/${instanceId}/agent-routes/${routeId}`,
        );
        await invalidate();
        toast.success(i18next.t("channels:detail.agentRoutes.toast.deleted"));
      } catch (err) {
        toast.error(
          i18next.t("channels:detail.agentRoutes.toast.failedDelete"),
          userFriendlyError(err),
        );
        throw err;
      }
    },
    [instanceId, http, invalidate],
  );

  return {
    routes: data ?? [],
    loading,
    error,
    createRoute,
    updateRoute,
    deleteRoute,
    refresh: invalidate,
  };
}
