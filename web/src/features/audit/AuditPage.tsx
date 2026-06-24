import { useQuery } from "@tanstack/react-query";
import { listAudit } from "@/api/admin";
import { PageHeader, QueryState } from "@/app/page";
import { fmtTime } from "@/lib/format";
import { Table, TBody, TD, TH, THead, TR } from "@/ui/table";

export function AuditPage() {
  const q = useQuery({ queryKey: ["audit"], queryFn: listAudit });
  const entries = q.data?.audit ?? [];

  return (
    <>
      <PageHeader title="Audit log" description="Administrative actions, newest first." />
      <QueryState
        isLoading={q.isLoading}
        error={q.error}
        empty={entries.length === 0}
        emptyText="No audit entries yet."
      >
        <Table>
          <THead>
            <TR>
              <TH>Time</TH>
              <TH>Actor</TH>
              <TH>Action</TH>
              <TH>Target</TH>
              <TH>IP</TH>
            </TR>
          </THead>
          <TBody>
            {entries.map((e) => (
              <TR key={e.AuditID}>
                <TD className="whitespace-nowrap text-muted-foreground">{fmtTime(e.CreatedAt)}</TD>
                <TD className="font-mono text-xs">{e.Actor}</TD>
                <TD>{e.Action}</TD>
                <TD className="font-mono text-xs" title={e.Target}>
                  {e.Target}
                </TD>
                <TD className="text-muted-foreground">{e.IP ?? "—"}</TD>
              </TR>
            ))}
          </TBody>
        </Table>
      </QueryState>
    </>
  );
}
