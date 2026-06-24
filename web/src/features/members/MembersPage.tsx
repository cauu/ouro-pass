import { useQuery, useQueryClient } from "@tanstack/react-query";
import { listMembers, revokeMember } from "@/api/admin";
import { PageHeader, QueryState } from "@/app/page";
import { useAuth } from "@/auth/AuthContext";
import { StepUpDialog } from "@/features/auth/StepUpDialog";
import { short } from "@/lib/format";
import { Badge } from "@/ui/badge";
import { Button } from "@/ui/button";
import { Table, TBody, TD, TH, THead, TR } from "@/ui/table";

export function MembersPage() {
  const qc = useQueryClient();
  const { role } = useAuth();
  const canRevoke = role === "operator" || role === "owner";
  const q = useQuery({ queryKey: ["members"], queryFn: listMembers });
  const members = q.data?.members ?? [];

  return (
    <>
      <PageHeader title="Members" description="Active members keyed by on-chain stake credential." />
      <QueryState isLoading={q.isLoading} error={q.error} empty={members.length === 0} emptyText="No members yet.">
        <Table>
          <THead>
            <TR>
              <TH>Stake credential</TH>
              <TH>Tier</TH>
              <TH>Channel</TH>
              {canRevoke ? <TH className="text-right">Actions</TH> : null}
            </TR>
          </THead>
          <TBody>
            {members.map((m) => (
              <TR key={`${m.stake_credential_hash}:${m.channel_type}`}>
                <TD className="font-mono text-xs" title={m.stake_credential_hash}>
                  {short(m.stake_credential_hash, 20)}
                </TD>
                <TD>
                  <Badge>{m.tier}</Badge>
                </TD>
                <TD>{m.channel_type}</TD>
                {canRevoke ? (
                  <TD className="text-right">
                    <StepUpDialog
                      trigger={
                        <Button variant="destructive" size="sm">
                          Revoke
                        </Button>
                      }
                      title="Revoke member"
                      description={`Blacklist ${short(m.stake_credential_hash)} and revoke all of their tokens, refresh grants, and subscriptions.`}
                      onConfirm={async (su) => {
                        await revokeMember(m.stake_credential_hash, su);
                      }}
                      onDone={() => qc.invalidateQueries({ queryKey: ["members"] })}
                    />
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
