import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useForm } from "react-hook-form";
import { configureChannel, listChannels } from "@/api/admin";
import { ApiError } from "@/api/client";
import { PageHeader } from "@/app/page";
import { Badge } from "@/ui/badge";
import { Button } from "@/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/ui/card";
import { Field } from "@/ui/field";
import { Input } from "@/ui/input";
import { useToast } from "@/ui/toast";

interface ChannelForm {
  botToken: string;
}

export function ChannelsPage() {
  const { toast } = useToast();
  const qc = useQueryClient();
  const channels = useQuery({ queryKey: ["channels"], queryFn: listChannels });
  const configured = channels.data?.channels.find((c) => c.channel_type === "telegram")?.configured ?? false;
  const { register, handleSubmit, reset } = useForm<ChannelForm>({ defaultValues: { botToken: "" } });

  const save = useMutation({
    // The configure endpoint stores the bot token encrypted at rest (field cipher).
    mutationFn: (v: ChannelForm) => configureChannel("telegram", { bot_token: v.botToken }),
    onSuccess: () => {
      toast({ title: "Telegram configured", variant: "success" });
      reset();
      void qc.invalidateQueries({ queryKey: ["channels"] });
    },
    onError: (e) =>
      toast({
        title: "Configure failed",
        description: e instanceof ApiError ? e.message : String(e),
        variant: "destructive",
      }),
  });

  return (
    <>
      <PageHeader title="Channels" description="Delivery channel configuration." />
      <Card className="max-w-lg">
        <CardHeader>
          <div className="flex items-center justify-between">
            <CardTitle>Telegram</CardTitle>
            <Badge variant={configured ? "success" : "muted"}>
              {configured ? "configured" : "not configured"}
            </Badge>
          </div>
          <CardDescription>
            Set the bot token. It is stored encrypted (field cipher) and never returned. Saving takes
            effect live — no restart. {configured ? "Submit a new token to replace the current one." : ""}
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form className="grid gap-3" onSubmit={handleSubmit((v) => save.mutate(v))}>
            <Field label="Bot token" hint="From @BotFather, e.g. 123456:ABC-DEF…">
              <Input type="password" autoComplete="off" {...register("botToken", { required: true })} />
            </Field>
            <div>
              <Button type="submit" disabled={save.isPending}>
                Save configuration
              </Button>
            </div>
          </form>
        </CardContent>
      </Card>
    </>
  );
}
