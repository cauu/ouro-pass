import { useMutation } from "@tanstack/react-query";
import { useForm } from "react-hook-form";
import { configureChannel } from "@/api/admin";
import { ApiError } from "@/api/client";
import { PageHeader } from "@/app/page";
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
  const { register, handleSubmit, reset } = useForm<ChannelForm>({ defaultValues: { botToken: "" } });

  const save = useMutation({
    // The configure endpoint stores an opaque encrypted config blob; for Telegram
    // that is the bot token.
    mutationFn: (v: ChannelForm) => configureChannel("telegram", { bot_token: v.botToken }),
    onSuccess: () => {
      toast({ title: "Telegram configured", variant: "success" });
      reset();
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
          <CardTitle>Telegram</CardTitle>
          <CardDescription>
            Set the bot token. It is stored encrypted (field cipher) and never returned.
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
