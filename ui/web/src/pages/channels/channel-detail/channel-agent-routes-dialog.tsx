import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import type {
  AgentRouteMediaType,
  AgentRoutePeerKind,
  AgentRouteTargetKind,
  ChannelAgentRoute,
  ChannelAgentRouteInput,
} from "@/types/channel";
import type { AgentData } from "@/types/agent";

interface AgentRouteDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  agents: AgentData[];
  initial: ChannelAgentRoute | null; // null = create mode
  onSubmit: (payload: ChannelAgentRouteInput) => Promise<void>;
}

const MEDIA_ANY = "__any__";

// Edit or create dialog. Tool allow editor: textarea (1 tool per line) plus an
// "Inherit all" checkbox. When inherit is on, the textarea is disabled and the
// payload sends tool_allow=null.
export function AgentRouteDialog({
  open,
  onOpenChange,
  agents,
  initial,
  onSubmit,
}: AgentRouteDialogProps) {
  const { t } = useTranslation("channels");
  const [name, setName] = useState("");
  const [agentID, setAgentID] = useState("");
  const [peerKind, setPeerKind] = useState<AgentRoutePeerKind>("direct");
  const [mediaType, setMediaType] = useState<string>(MEDIA_ANY);
  const [mentionRequired, setMentionRequired] = useState(false);
  const [priority, setPriority] = useState(100);
  const [isEnabled, setIsEnabled] = useState(true);
  const [inheritTools, setInheritTools] = useState(true);
  const [toolsText, setToolsText] = useState("");
  const [intent, setIntent] = useState("");
  const [targetKind, setTargetKind] = useState<AgentRouteTargetKind>("agent");
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    if (!open) return;
    if (initial) {
      setName(initial.name);
      setAgentID(initial.agent_id);
      setPeerKind(initial.peer_kind);
      setMediaType(initial.media_type ?? MEDIA_ANY);
      setMentionRequired(initial.mention_required);
      setPriority(initial.priority);
      setIsEnabled(initial.is_enabled);
      setInheritTools(initial.tool_allow === null);
      setToolsText((initial.tool_allow ?? []).join("\n"));
      setIntent(initial.intent ?? "");
      setTargetKind(initial.target_kind);
    } else {
      setName("");
      setAgentID(agents[0]?.id ?? "");
      setPeerKind("direct");
      setMediaType(MEDIA_ANY);
      setMentionRequired(false);
      setPriority(100);
      setIsEnabled(true);
      setInheritTools(true);
      setToolsText("");
      setIntent("");
      setTargetKind("agent");
    }
  }, [open, initial, agents]);

  const submit = async () => {
    if (!agentID) return;
    setSubmitting(true);
    try {
      const tools = inheritTools
        ? null
        : toolsText
            .split("\n")
            .map((s) => s.trim())
            .filter(Boolean);
      const payload: ChannelAgentRouteInput = {
        name: name.trim(),
        agent_id: agentID,
        peer_kind: peerKind,
        media_type: mediaType === MEDIA_ANY ? null : (mediaType as AgentRouteMediaType),
        mention_required: mentionRequired,
        priority,
        is_enabled: isEnabled,
        tool_allow: tools,
        intent: intent.trim() === "" ? null : intent.trim(),
        target_kind: targetKind,
      };
      await onSubmit(payload);
      onOpenChange(false);
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg max-sm:inset-0">
        <DialogHeader>
          <DialogTitle>{initial ? t("detail.agentRoutes.dialog.editTitle") : t("detail.agentRoutes.dialog.createTitle")}</DialogTitle>
        </DialogHeader>
        <div className="space-y-3 py-2">
          <div className="space-y-1">
            <Label>{t("detail.agentRoutes.fields.name")}</Label>
            <Input className="text-base md:text-sm" value={name} onChange={(e) => setName(e.target.value)} placeholder={t("detail.agentRoutes.placeholders.name")} />
          </div>
          <div className="space-y-1">
            <Label>{t("detail.agentRoutes.fields.agent")}</Label>
            <Select value={agentID} onValueChange={setAgentID}>
              <SelectTrigger className="text-base md:text-sm">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {agents.map((a) => (
                  <SelectItem key={a.id} value={a.id}>{a.display_name || a.agent_key}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
            <div className="space-y-1">
              <Label>{t("detail.agentRoutes.fields.peerKind")}</Label>
              <Select value={peerKind} onValueChange={(v) => setPeerKind(v as AgentRoutePeerKind)}>
                <SelectTrigger className="text-base md:text-sm"><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="direct">{t("detail.agentRoutes.peerKind.direct")}</SelectItem>
                  <SelectItem value="group">{t("detail.agentRoutes.peerKind.group")}</SelectItem>
                  <SelectItem value="supergroup">{t("detail.agentRoutes.peerKind.supergroup")}</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1">
              <Label>{t("detail.agentRoutes.fields.mediaType")}</Label>
              <Select value={mediaType} onValueChange={setMediaType}>
                <SelectTrigger className="text-base md:text-sm"><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value={MEDIA_ANY}>{t("detail.agentRoutes.mediaType.any")}</SelectItem>
                  <SelectItem value="text">{t("detail.agentRoutes.mediaType.text")}</SelectItem>
                  <SelectItem value="voice">{t("detail.agentRoutes.mediaType.voice")}</SelectItem>
                  <SelectItem value="media">{t("detail.agentRoutes.mediaType.media")}</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
            <div className="space-y-1">
              <Label>{t("detail.agentRoutes.fields.priority")}</Label>
              <Input
                type="number"
                inputMode="numeric"
                className="text-base md:text-sm"
                value={priority}
                onChange={(e) => setPriority(Number(e.target.value))}
              />
            </div>
            <div className="flex items-center justify-between gap-3 pt-6">
              <Label className="font-normal">{t("detail.agentRoutes.fields.mentionRequired")}</Label>
              <Switch checked={mentionRequired} onCheckedChange={setMentionRequired} />
            </div>
          </div>
          <div className="space-y-1">
            <div className="flex items-center justify-between">
              <Label>{t("detail.agentRoutes.fields.toolAllow")}</Label>
              <div className="flex items-center gap-2 text-xs text-muted-foreground">
                <span>{t("detail.agentRoutes.toolAllow.inheritToggle")}</span>
                <Switch checked={inheritTools} onCheckedChange={setInheritTools} />
              </div>
            </div>
            <Textarea
              className="text-base md:text-sm min-h-[96px] font-mono"
              value={toolsText}
              onChange={(e) => setToolsText(e.target.value)}
              disabled={inheritTools}
              placeholder={t("detail.agentRoutes.placeholders.toolAllow")}
            />
            <p className="text-xs text-muted-foreground">{t("detail.agentRoutes.toolAllow.hint")}</p>
          </div>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
            <div className="space-y-1">
              <Label>{t("detail.agentRoutes.fields.targetKind")}</Label>
              <Select value={targetKind} onValueChange={(v) => setTargetKind(v as AgentRouteTargetKind)}>
                <SelectTrigger className="text-base md:text-sm"><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="agent">{t("detail.agentRoutes.targetKind.agent")}</SelectItem>
                  <SelectItem value="team">{t("detail.agentRoutes.targetKind.team")}</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1">
              <Label>{t("detail.agentRoutes.fields.intent")}</Label>
              <Input
                className="text-base md:text-sm"
                value={intent}
                onChange={(e) => setIntent(e.target.value)}
                placeholder={t("detail.agentRoutes.placeholders.intent")}
              />
            </div>
          </div>
          <p className="text-xs text-muted-foreground">
            {t("detail.agentRoutes.fields.intentHint")}
          </p>
          <div className="flex items-center justify-between pt-1">
            <Label className="font-normal">{t("detail.agentRoutes.fields.isEnabled")}</Label>
            <Switch checked={isEnabled} onCheckedChange={setIsEnabled} />
          </div>
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={submitting}>
            {t("detail.agentRoutes.actions.cancel")}
          </Button>
          <Button onClick={submit} disabled={submitting || !agentID}>
            {initial ? t("detail.agentRoutes.actions.save") : t("detail.agentRoutes.actions.create")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
