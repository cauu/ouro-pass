import { useQuery } from "@tanstack/react-query";
import { listAudit } from "@/api/admin";
import { PageHeader, QueryState } from "@/app/page";
import { fmtTime } from "@/lib/format";
import { Badge } from "@/ui/badge";
import { Table, TBody, TD, TH, THead, TR } from "@/ui/table";

export function AuditPage() {
  const q = useQuery({ queryKey: ["audit"], queryFn: listAudit });
  const entries = q.data?.audit ?? [];

  return (
    <>
      <PageHeader
        title="Audit log"
        description="Immutable record of administrative actions, newest first. Owner-only."
      />
      <QueryState
        isLoading={q.isLoading}
        error={q.error}
        empty={entries.length === 0}
        emptyText="No audit entries yet. Sensitive actions are recorded here as they happen."
      >
        <Table footer={<span>{entries.length} entr{entries.length === 1 ? "y" : "ies"}</span>}>
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
                <TD>
                  <Badge variant="outline" className="font-mono text-[11px]">
                    {e.Action}
                  </Badge>
                </TD>
                <TD className="max-w-xs truncate font-mono text-xs" title={e.Target}>
                  {e.Target}
                </TD>
                <TD className="font-mono text-xs text-muted-foreground">{e.IP ?? "—"}</TD>
              </TR>
            ))}
          </TBody>
        </Table>
      </QueryState>
    </>
  );
}
