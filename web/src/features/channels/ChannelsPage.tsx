import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
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
import { Badge } from "@/ui/badge";
import { Button } from "@/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/ui/card";
import { Field } from "@/ui/field";
import { Input } from "@/ui/input";
import { Table, TBody, TD, TH, THead, TR } from "@/ui/table";
import { useToast } from "@/ui/toast";

interface ChannelForm {
  name: string;
  botToken: string;
  botUsername: string;
}

// ChannelsPage manages a pool's Telegram channel instances (S0005). A pool may
// run several bots (e.g. a members bot and an announcements bot); each instance
// has a stable id, a unique name, its own encrypted token, and its own worker.
export function ChannelsPage() {
  const { toast } = useToast();
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["channels"], queryFn: listChannels });
  const instances = q.data?.channels ?? [];

  const { register, handleSubmit, reset } = useForm<ChannelForm>({
    defaultValues: { name: "", botToken: "", botUsername: "" },
  });

  const invalidate = () => void qc.invalidateQueries({ queryKey: ["channels"] });
  const onErr = (e: unknown) =>
    toast({
      title: "Action failed",
      description: e instanceof ApiError ? e.message : String(e),
      variant: "destructive",
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
      invalidate();
    },
    onError: onErr,
  });

  const toggle = useMutation({
    mutationFn: (c: ChannelInstance) => setChannelEnabled(c.channel_id, c.status !== "active"),
    onSuccess: invalidate,
    onError: onErr,
  });

  const retoken = useMutation({
    mutationFn: ({ id, token }: { id: string; token: string }) =>
      updateChannel(id, { config: { bot_token: token } }),
    onSuccess: () => {
      toast({ title: "Token updated", variant: "success" });
      invalidate();
    },
    onError: onErr,
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
    onError: onErr,
  });

  return (
    <>
      <PageHeader
        title="Channels"
        description="Telegram delivery instances. Run as many bots as you need — each instance has its own token and worker, and members/activation/push bind to a specific instance."
      />

      <Card className="mb-4 max-w-lg">
        <CardHeader>
          <CardTitle>Add Telegram instance</CardTitle>
          <CardDescription>
            The bot token is stored encrypted (field cipher) and never returned. Saving takes effect
            live — no restart.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form className="grid gap-3" onSubmit={handleSubmit((v) => create.mutate(v))}>
            <Field label="Name" hint="A unique label for this instance, e.g. members or announcements">
              <Input autoComplete="off" {...register("name", { required: true })} />
            </Field>
            <Field label="Bot token" hint="From @BotFather, e.g. 123456:ABC-DEF…">
              <Input type="password" autoComplete="off" {...register("botToken", { required: true })} />
            </Field>
            <Field label="Bot username" hint="Public @username (no @), used for activation deep links">
              <Input autoComplete="off" {...register("botUsername")} />
            </Field>
            <div>
              <Button type="submit" disabled={create.isPending}>
                Add instance
              </Button>
            </div>
          </form>
        </CardContent>
      </Card>

      <QueryState isLoading={q.isLoading} error={q.error}>
        <Card>
          <CardHeader>
            <CardTitle>Configured instances</CardTitle>
            <CardDescription>
              {instances.length === 0
                ? "None yet — add a Telegram instance to deliver memberships."
                : `${instances.length} configured.`}
            </CardDescription>
          </CardHeader>
          <CardContent>
            <Table>
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
                    <TD>{c.name}</TD>
                    <TD className="font-mono text-xs">{c.channel_type}</TD>
                    <TD className="font-mono text-xs">{c.bot_username ? `@${c.bot_username}` : "—"}</TD>
                    <TD className="font-mono text-xs text-muted-foreground" title="First and last 4 characters of the bot token (the full token is never shown)">
                      {c.token_hint || "—"}
                    </TD>
                    <TD>
                      <Badge variant={c.status === "active" ? "success" : "muted"}>{c.status}</Badge>
                    </TD>
                    <TD className="space-x-2 text-right">
                      <Button variant="ghost" onClick={() => toggle.mutate(c)} disabled={toggle.isPending}>
                        {c.status === "active" ? "Disable" : "Enable"}
                      </Button>
                      <Button
                        variant="ghost"
                        onClick={() => {
                          const token = prompt(`New bot token for "${c.name}"?`);
                          if (token) retoken.mutate({ id: c.channel_id, token });
                        }}
                        disabled={retoken.isPending}
                      >
                        Re-token
                      </Button>
                      <Button
                        variant="ghost"
                        onClick={() => {
                          if (confirm(`Remove instance "${c.name}"? Active subscriptions are cancelled.`))
                            remove.mutate(c.channel_id);
                        }}
                        disabled={remove.isPending}
                      >
                        Delete
                      </Button>
                    </TD>
                  </TR>
                ))}
              </TBody>
            </Table>
          </CardContent>
        </Card>
      </QueryState>
    </>
  );
}
