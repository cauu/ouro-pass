import { useQuery } from "@tanstack/react-query";
import { fetchJwks, listMembers, listSubscriptions } from "@/api/admin";
import { PageHeader } from "@/app/page";
import { useAuth } from "@/auth/AuthContext";
import { Card, CardContent, CardDescription, CardTitle } from "@/ui/card";

function StatCard({ label, value, hint }: { label: string; value: string | number; hint?: string }) {
  return (
    <Card>
      <CardContent className="pt-5">
        <CardDescription>{label}</CardDescription>
        <CardTitle className="mt-1 text-3xl tabular-nums">{value}</CardTitle>
        {hint ? <p className="mt-1 text-xs text-muted-foreground">{hint}</p> : null}
      </CardContent>
    </Card>
  );
}

export function DashboardPage() {
  const { role } = useAuth();
  const members = useQuery({ queryKey: ["members"], queryFn: listMembers });
  const subs = useQuery({ queryKey: ["subscriptions"], queryFn: listSubscriptions });
  const jwks = useQuery({ queryKey: ["jwks"], queryFn: fetchJwks });

  const activeSubs = (subs.data?.subscriptions ?? []).filter((s) => s.Status === "active").length;

  return (
    <>
      <PageHeader title="Dashboard" description={`Signed in as ${role ?? "—"}.`} />
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        <StatCard label="Members" value={members.data?.members.length ?? "…"} />
        <StatCard label="Active subscriptions" value={subs.isLoading ? "…" : activeSubs} />
        <StatCard
          label="Signing keys (JWKS)"
          value={jwks.data?.keys.length ?? "…"}
          hint="Keys currently advertised at /.well-known/ouropass/jwks.json"
        />
      </div>
    </>
  );
}
