import { useQuery } from "@tanstack/react-query";
import { Check, Circle } from "lucide-react";
import { Link } from "react-router-dom";
import { fetchJwks, listChannels, listClients } from "@/api/admin";
import { PageHeader } from "@/app/page";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/ui/card";

function Step({ done, title, children }: { done: boolean; title: string; children: React.ReactNode }) {
  return (
    <div className="flex gap-3">
      <div className="mt-0.5">
        {done ? (
          <Check className="h-5 w-5 text-success" />
        ) : (
          <Circle className="h-5 w-5 text-muted-foreground" />
        )}
      </div>
      <div>
        <div className="font-medium">{title}</div>
        <div className="text-sm text-muted-foreground">{children}</div>
      </div>
    </div>
  );
}

export function SetupPage() {
  const jwks = useQuery({ queryKey: ["jwks"], queryFn: fetchJwks });
  const clients = useQuery({ queryKey: ["clients"], queryFn: listClients });
  const channels = useQuery({ queryKey: ["channels"], queryFn: listChannels });

  const hasKey = (jwks.data?.keys?.length ?? 0) > 0;
  const hasClient = (clients.data?.clients?.length ?? 0) > 0;
  const hasTelegram =
    channels.data?.channels.some((c) => c.channel_type === "telegram" && c.configured) ?? false;

  return (
    <>
      <PageHeader title="Setup" description="Bring the issuer online for your pool." />
      <Card className="max-w-2xl">
        <CardHeader>
          <CardTitle>Checklist</CardTitle>
          <CardDescription>You are already signed in as owner — the hard part is done.</CardDescription>
        </CardHeader>
        <CardContent className="grid gap-4">
          <Step done={hasKey} title="Signing key">
            {hasKey ? (
              "An active signing key is published in JWKS."
            ) : (
              <>
                Generate one on the <Link className="underline" to="/keys">Signing Keys</Link> page to enable token
                issuance.
              </>
            )}
          </Step>
          <Step done={hasClient} title="OAuth client">
            {hasClient ? (
              "At least one client can request logins."
            ) : (
              <>
                Register one on the <Link className="underline" to="/clients">OAuth Clients</Link> page.
              </>
            )}
          </Step>
          <Step done={hasTelegram} title="Telegram channel">
            {hasTelegram ? (
              "The Telegram bot token is configured — memberships can be delivered."
            ) : (
              <>
                Configure the bot token on the <Link className="underline" to="/channels">Channels</Link> page to
                deliver memberships.
              </>
            )}
          </Step>
        </CardContent>
      </Card>
    </>
  );
}
