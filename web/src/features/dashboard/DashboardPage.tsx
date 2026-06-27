import { useQuery } from "@tanstack/react-query";
import { AlertCircle, BellRing, Check, KeyRound, Users } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { Link } from "react-router-dom";
import { fetchJwks, listChannels, listClients, listMembers, listSubscriptions } from "@/api/admin";
import { PageHeader } from "@/app/page";
import { useAuth } from "@/auth/AuthContext";
import { cn } from "@/lib/cn";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/ui/card";
import { Skeleton } from "@/ui/skeleton";
import { StatusBadge } from "@/ui/status-badge";

function StatCard({
  label,
  icon: Icon,
  loading,
  error,
  value,
  hint,
}: {
  label: string;
  icon: LucideIcon;
  loading: boolean;
  error?: unknown;
  value: React.ReactNode;
  hint?: string;
}) {
  return (
    <Card>
      <CardContent className="pt-5">
        <div className="flex items-center gap-2 text-sm text-muted-foreground">
          <Icon className="h-4 w-4" />
          {label}
        </div>
        <div className="mt-2 text-3xl font-semibold tabular-nums">
          {loading ? (
            <Skeleton className="h-8 w-16" />
          ) : error ? (
            <span className="inline-flex items-center gap-1.5 text-base font-normal text-destructive">
              <AlertCircle className="h-4 w-4" />
              Failed to load
            </span>
          ) : (
            value
          )}
        </div>
        {hint && !error ? <p className="mt-1.5 text-xs text-muted-foreground">{hint}</p> : null}
      </CardContent>
    </Card>
  );
}

// QuickStart folds the former Setup checklist into the Dashboard (S0008 p1-2):
// it shows the three onboarding steps and disappears once all are done. Owner-only
// (the steps are owner/operator actions; matches the old /setup RBAC).
function QuickStartStep({
  done,
  title,
  children,
}: {
  done: boolean;
  title: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex gap-3">
      <div
        className={cn(
          "mt-0.5 grid h-6 w-6 shrink-0 place-items-center rounded-full",
          done ? "bg-success/15 text-success" : "border-2 text-muted-foreground",
        )}
      >
        {done ? <Check className="h-3.5 w-3.5" strokeWidth={3} /> : null}
      </div>
      <div>
        <div className="text-sm font-medium">{title}</div>
        <div className="mt-0.5 text-sm text-muted-foreground">{children}</div>
      </div>
    </div>
  );
}

function QuickStart() {
  const jwks = useQuery({ queryKey: ["jwks"], queryFn: fetchJwks });
  const clients = useQuery({ queryKey: ["clients"], queryFn: listClients });
  const channels = useQuery({ queryKey: ["channels"], queryFn: listChannels });

  const hasKey = (jwks.data?.keys?.length ?? 0) > 0;
  const hasClient = (clients.data?.clients?.length ?? 0) > 0;
  const hasTelegram =
    channels.data?.channels.some((c) => c.channel_type === "telegram" && c.configured) ?? false;

  // Hold until all three queries resolve, then hide entirely once onboarding is done.
  const ready = !jwks.isLoading && !clients.isLoading && !channels.isLoading;
  if (!ready || (hasKey && hasClient && hasTelegram)) return null;

  return (
    <Card className="mb-4 max-w-2xl border-primary/30 bg-primary/5">
      <CardHeader>
        <CardTitle className="text-sm">Quick start</CardTitle>
        <CardDescription>
          Finish bringing the issuer online — this card disappears once all steps are done.
        </CardDescription>
      </CardHeader>
      <CardContent className="grid gap-4">
        <QuickStartStep done={hasKey} title="Signing key">
          {hasKey ? (
            "An active signing key is published in JWKS."
          ) : (
            <>
              Generate one on the{" "}
              <Link className="underline" to="/keys">
                Signing Keys
              </Link>{" "}
              page to enable token issuance.
            </>
          )}
        </QuickStartStep>
        <QuickStartStep done={hasClient} title="OAuth client">
          {hasClient ? (
            "At least one client can request logins."
          ) : (
            <>
              Register one on the{" "}
              <Link className="underline" to="/clients">
                OAuth Clients
              </Link>{" "}
              page.
            </>
          )}
        </QuickStartStep>
        <QuickStartStep done={hasTelegram} title="Telegram channel">
          {hasTelegram ? (
            "The Telegram bot token is configured — memberships can be delivered."
          ) : (
            <>
              Configure the bot token on the{" "}
              <Link className="underline" to="/channels">
                Channels
              </Link>{" "}
              page to deliver memberships.
            </>
          )}
        </QuickStartStep>
      </CardContent>
    </Card>
  );
}

export function DashboardPage() {
  const { role } = useAuth();
  const members = useQuery({ queryKey: ["members"], queryFn: listMembers });
  const subs = useQuery({ queryKey: ["subscriptions"], queryFn: listSubscriptions });
  const jwks = useQuery({ queryKey: ["jwks"], queryFn: fetchJwks });

  const memberRows = members.data?.members ?? [];
  const activeSubs = (subs.data?.subscriptions ?? []).filter((s) => s.Status === "active").length;
  const keyCount = jwks.data?.keys?.length ?? 0;

  // Tier distribution is derived client-side from the roster (each member row
  // already carries `tier`) — no extra endpoint needed.
  const tierCounts = memberRows.reduce<Record<string, number>>((acc, m) => {
    acc[m.tier] = (acc[m.tier] ?? 0) + 1;
    return acc;
  }, {});
  const tiers = Object.entries(tierCounts).sort((a, b) => b[1] - a[1]);
  const maxTier = tiers.reduce((m, [, n]) => Math.max(m, n), 0);

  return (
    <>
      <PageHeader title="Dashboard" description={`Signed in as ${role ?? "—"}.`} />

      {role === "owner" ? <QuickStart /> : null}

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        <StatCard
          label="Members"
          icon={Users}
          loading={members.isLoading}
          error={members.error}
          value={memberRows.length}
          hint="Eligible members derived from the active snapshot"
        />
        <StatCard
          label="Active subscriptions"
          icon={BellRing}
          loading={subs.isLoading}
          error={subs.error}
          value={activeSubs}
          hint="Sessions currently in active status"
        />
        <StatCard
          label="Signing keys (JWKS)"
          icon={KeyRound}
          loading={jwks.isLoading}
          error={jwks.error}
          value={
            <span className="flex items-center gap-2">
              <span>{keyCount}</span>
              <StatusBadge
                status={keyCount > 0 ? "active" : "failed"}
                label={keyCount > 0 ? "healthy" : "no active key"}
              />
            </span>
          }
          hint="Published at /.well-known/ouropass/jwks.json"
        />
      </div>

      <Card className="mt-4 max-w-xl">
        <CardHeader>
          <CardTitle className="text-sm">Tier distribution</CardTitle>
        </CardHeader>
        <CardContent>
          {members.isLoading ? (
            <div className="space-y-3">
              {Array.from({ length: 3 }).map((_, i) => (
                <Skeleton key={i} className="h-4 w-full" />
              ))}
            </div>
          ) : members.error ? (
            <p className="flex items-center gap-1.5 text-sm text-destructive">
              <AlertCircle className="h-4 w-4" />
              Failed to load members.
            </p>
          ) : tiers.length === 0 ? (
            <p className="text-sm text-muted-foreground">No members yet.</p>
          ) : (
            <div className="space-y-3">
              {tiers.map(([tier, n]) => (
                <div key={tier} className="flex items-center gap-3 text-sm">
                  <span className="w-20 shrink-0 capitalize">{tier}</span>
                  <div className="h-2 flex-1 overflow-hidden rounded-full bg-muted">
                    <div
                      className="h-full rounded-full bg-primary"
                      style={{ width: `${maxTier ? (n / maxTier) * 100 : 0}%` }}
                    />
                  </div>
                  <span className="w-10 shrink-0 text-right tabular-nums text-muted-foreground">
                    {n}
                  </span>
                </div>
              ))}
            </div>
          )}
        </CardContent>
      </Card>
    </>
  );
}
