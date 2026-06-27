import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { cancelSubscription, listSubscriptions } from "@/api/admin";
import { ApiError } from "@/api/client";
import { PageHeader, QueryState } from "@/app/page";
import { useAuth } from "@/auth/AuthContext";
import { fmtTime, short } from "@/lib/format";
import { Badge } from "@/ui/badge";
import { Button } from "@/ui/button";
import { CopyButton } from "@/ui/copy-button";
import { StatusBadge } from "@/ui/status-badge";
import { Table, TBody, TD, TH, THead, TR } from "@/ui/table";
import { useToast } from "@/ui/toast";

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
      <PageHeader
        title="Subscriptions"
        description="Per-channel membership sessions. The reconciliation job downgrades or expires them on epoch boundaries; cancel is available while active."
      />
      <QueryState
        isLoading={q.isLoading}
        error={q.error}
        empty={subs.length === 0}
        emptyText="No subscriptions yet. They appear when members activate a delivery channel."
      >
        <Table footer={<span>{subs.length} session(s)</span>}>
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
                <TD>
                  <CopyButton
                    value={s.StakeCredentialHash}
                    display={short(s.StakeCredentialHash, 18)}
                    toastLabel="Stake credential copied"
                  />
                </TD>
                <TD className="text-muted-foreground">{s.ChannelType}</TD>
                <TD>
                  <Badge className="capitalize">{s.Tier}</Badge>
                </TD>
                <TD>
                  <StatusBadge status={s.Status} />
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
