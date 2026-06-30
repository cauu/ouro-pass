import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useForm } from "react-hook-form";
import {
  createChannel,
  deleteChannel,
  listChannels,
  setChannelEnabled,
  updateChannel,
} from "@/api/admin";
import { ApiError } from "@/api/client";
import { PageHeader, QueryState } from "@/app/page";
import type { ChannelInstance } from "@/lib/types";
import { Button } from "@/ui/button";
import { ConfirmDialog, PromptDialog } from "@/ui/confirm-dialog";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/ui/dialog";
import { Field } from "@/ui/field";
import { Input } from "@/ui/input";
import { StatusBadge } from "@/ui/status-badge";
import { Table, TBody, TD, TH, THead, TR } from "@/ui/table";
import { useToast } from "@/ui/toast";

interface ChannelForm {
  name: string;
  botToken: string;
  botUsername: string;
}

// CreateChannelDialog hosts the "add Telegram instance" form in a modal (S0008
// p1-1): the list is the page subject, adding an instance is an occasional action.
function CreateChannelDialog({ onCreated }: { onCreated: () => void }) {
  const [open, setOpen] = useState(false);
  const { toast } = useToast();
  const {
    register,
    handleSubmit,
    reset,
    formState: { errors },
  } = useForm<ChannelForm>({
    defaultValues: { name: "", botToken: "", botUsername: "" },
  });

  const create = useMutation({
    // The token is stored encrypted (field cipher) and never returned; the bot
    // username is public and used for activation deep links.
    mutationFn: (v: ChannelForm) =>
      createChannel({
        channel_type: "telegram",
        name: v.name,
        config: { bot_token: v.botToken, bot_username: v.botUsername || undefined },
      }),
    onSuccess: () => {
      toast({ title: "Telegram instance added", variant: "success" });
      reset();
      setOpen(false);
      onCreated();
    },
    onError: (e) =>
      toast({
        title: "Action failed",
        description: e instanceof ApiError ? e.message : String(e),
        variant: "destructive",
      }),
  });

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="sm">Add instance</Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Add Telegram instance</DialogTitle>
          <DialogDescription>
            The bot token is stored encrypted (field cipher) and never returned. Saving takes effect
            live — no restart.
          </DialogDescription>
        </DialogHeader>
        <form className="grid gap-3" onSubmit={handleSubmit((v) => create.mutate(v))}>
          <Field
            label="Name"
            required
            hint="A unique label for this instance, e.g. members or announcements"
            error={errors.name && "Name is required"}
          >
            <Input autoComplete="off" {...register("name", { required: true })} />
          </Field>
          <Field
            label="Bot token"
            required
            hint="From @BotFather, e.g. 123456:ABC-DEF…"
            error={errors.botToken && "Bot token is required"}
          >
            <Input type="password" autoComplete="off" {...register("botToken", { required: true })} />
          </Field>
          <Field label="Bot username" hint="Public @username (no @), used for activation deep links">
            <Input autoComplete="off" {...register("botUsername")} />
          </Field>
          <DialogFooter>
            <Button type="submit" disabled={create.isPending}>
              Add instance
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

// ChannelsPage manages a pool's Telegram channel instances (S0005). A pool may
// run several bots (e.g. a members bot and an announcements bot); each instance
// has a stable id, a unique name, its own encrypted token, and its own worker.
export function ChannelsPage() {
  const { toast } = useToast();
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["channels"], queryFn: listChannels });
  const instances = q.data?.channels ?? [];

  const invalidate = () => void qc.invalidateQueries({ queryKey: ["channels"] });
  const onErr = (e: unknown) =>
    toast({
      title: "Action failed",
      description: e instanceof ApiError ? e.message : String(e),
      variant: "destructive",
    });

  const toggle = useMutation({
    mutationFn: (c: ChannelInstance) => setChannelEnabled(c.channel_id, c.status !== "active"),
    onSuccess: invalidate,
    onError: onErr,
  });

  // retoken/remove are only invoked through Prompt/ConfirmDialog, which surfaces
  // failures itself — no onError here, or the dialog's catch double-toasts.
  const retoken = useMutation({
    mutationFn: ({ id, token }: { id: string; token: string }) =>
      updateChannel(id, { config: { bot_token: token } }),
    onSuccess: () => {
      toast({ title: "Token updated", variant: "success" });
      invalidate();
    },
  });

  const remove = useMutation({
    mutationFn: (id: string) => deleteChannel(id),
    onSuccess: (r) => {
      toast({
        title: "Instance removed",
        description: `${r.sessions_cancelled} subscription(s) cancelled.`,
        variant: "success",
      });
      invalidate();
    },
  });

  // The per-instance bind link (S0016): holders who open it get an activation deep
  // link to THIS instance's bot, so the subscription binds here. Without the
  // channel_id, /bind falls back to the deployment-wide default bot.
  const copyBindLink = async (c: ChannelInstance) => {
    const link = `${window.location.origin}/bind?channel_id=${encodeURIComponent(c.channel_id)}`;
    try {
      await navigator.clipboard.writeText(link);
      toast({ title: "Bind link copied", description: link, variant: "success" });
    } catch {
      toast({ title: "Copy failed — copy it manually", description: link, variant: "destructive" });
    }
  };

  return (
    <>
      <PageHeader
        title="Channels"
        description="Telegram delivery instances. Run as many bots as you need — each instance has its own token and worker, and members/activation/push bind to a specific instance."
        action={<CreateChannelDialog onCreated={invalidate} />}
      />

      <QueryState isLoading={q.isLoading} error={q.error} empty={instances.length === 0} emptyText="None yet — add a Telegram instance to deliver memberships.">
        <Table footer={<span>{instances.length} instance(s)</span>}>
          <THead>
            <TR>
              <TH>Name</TH>
              <TH>Type</TH>
              <TH>Bot</TH>
              <TH>Token</TH>
              <TH>Status</TH>
              <TH className="text-right">Actions</TH>
            </TR>
          </THead>
          <TBody>
            {instances.map((c) => (
              <TR key={c.channel_id}>
                <TD className="font-medium">{c.name}</TD>
                <TD className="font-mono text-xs">{c.channel_type}</TD>
                <TD className="font-mono text-xs">{c.bot_username ? `@${c.bot_username}` : "—"}</TD>
                <TD className="font-mono text-xs text-muted-foreground" title="First and last 4 characters of the bot token (the full token is never shown)">
                  {c.token_hint || "—"}
                </TD>
                <TD>
                  <StatusBadge status={c.status} />
                </TD>
                <TD className="space-x-2 text-right">
                  {c.channel_type === "telegram" && (
                    <Button variant="ghost" onClick={() => copyBindLink(c)}>
                      Copy bind link
                    </Button>
                  )}
                  <Button variant="ghost" onClick={() => toggle.mutate(c)} disabled={toggle.isPending}>
                    {c.status === "active" ? "Disable" : "Enable"}
                  </Button>
                  <PromptDialog
                    trigger={<Button variant="ghost">Re-token</Button>}
                    title={`Re-token "${c.name}"`}
                    description="Set a new bot token for this instance. The previous token stops working immediately."
                    label="New bot token"
                    placeholder="123456:ABC-DEF…"
                    inputType="password"
                    confirmLabel="Save token"
                    onConfirm={(token) => retoken.mutateAsync({ id: c.channel_id, token })}
                  />
                  <ConfirmDialog
                    trigger={<Button variant="ghost">Delete</Button>}
                    title={`Remove instance "${c.name}"?`}
                    description="Active subscriptions on this instance are cancelled. This cannot be undone."
                    confirmLabel="Delete instance"
                    destructive
                    onConfirm={() => remove.mutateAsync(c.channel_id)}
                  />
                </TD>
              </TR>
            ))}
          </TBody>
        </Table>
      </QueryState>
    </>
  );
}
