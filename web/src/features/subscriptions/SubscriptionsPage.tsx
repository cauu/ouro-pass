import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { cancelSubscription, listSubscriptions } from "@/api/admin";
import { ApiError } from "@/api/client";
import { PageHeader, QueryState } from "@/app/page";
import { useAuth } from "@/auth/AuthContext";
import { fmtTime, short } from "@/lib/format";
import { Badge } from "@/ui/badge";
import { Button } from "@/ui/button";
import { Table, TBody, TD, TH, THead, TR } from "@/ui/table";
import { useToast } from "@/ui/toast";

function statusVariant(s: string): "success" | "destructive" | "muted" {
  if (s === "active") return "success";
  if (s === "cancelled" || s === "expired") return "destructive";
  return "muted";
}

export function SubscriptionsPage() {
  const qc = useQueryClient();
  const { toast } = useToast();
  const { role } = useAuth();
  const canCancel = role === "operator" || role === "owner";
  const q = useQuery({ queryKey: ["subscriptions"], queryFn: listSubscriptions });
  const subs = q.data?.subscriptions ?? [];

  const cancel = useMutation({
    mutationFn: cancelSubscription,
    onSuccess: () => {
      toast({ title: "Subscription cancelled", variant: "success" });
      qc.invalidateQueries({ queryKey: ["subscriptions"] });
    },
    onError: (e) =>
      toast({
        title: "Cancel failed",
        description: e instanceof ApiError ? e.message : String(e),
        variant: "destructive",
      }),
  });

  return (
    <>
      <PageHeader title="Subscriptions" description="Per-channel membership sessions." />
      <QueryState
        isLoading={q.isLoading}
        error={q.error}
        empty={subs.length === 0}
        emptyText="No subscriptions yet."
      >
        <Table>
          <THead>
            <TR>
              <TH>Stake credential</TH>
              <TH>Channel</TH>
              <TH>Tier</TH>
              <TH>Status</TH>
              <TH>Expires</TH>
              {canCancel ? <TH className="text-right">Actions</TH> : null}
            </TR>
          </THead>
          <TBody>
            {subs.map((s) => (
              <TR key={s.SessionID}>
                <TD className="font-mono text-xs" title={s.StakeCredentialHash}>
                  {short(s.StakeCredentialHash, 18)}
                </TD>
                <TD>{s.ChannelType}</TD>
                <TD>
                  <Badge>{s.Tier}</Badge>
                </TD>
                <TD>
                  <Badge variant={statusVariant(s.Status)}>{s.Status}</Badge>
                </TD>
                <TD className="text-muted-foreground">{fmtTime(s.ExpiresAt)}</TD>
                {canCancel ? (
                  <TD className="text-right">
                    <Button
                      variant="outline"
                      size="sm"
                      disabled={s.Status !== "active" || cancel.isPending}
                      onClick={() => cancel.mutate(s.SessionID)}
                    >
                      Cancel
                    </Button>
                  </TD>
                ) : null}
              </TR>
            ))}
          </TBody>
        </Table>
      </QueryState>
    </>
  );
}
