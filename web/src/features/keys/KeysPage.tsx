import { useQuery, useQueryClient } from "@tanstack/react-query";
import { fetchJwks, generateKey, retireKey, rotateKey } from "@/api/admin";
import { PageHeader, QueryState } from "@/app/page";
import { StepUpDialog } from "@/features/auth/StepUpDialog";
import { Badge } from "@/ui/badge";
import { Button } from "@/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/ui/card";
import { CopyButton } from "@/ui/copy-button";
import { StatusBadge } from "@/ui/status-badge";
import { Table, TBody, TD, TH, THead, TR } from "@/ui/table";

export function KeysPage() {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["jwks"], queryFn: fetchJwks });
  const keys = q.data?.keys ?? [];
  const refresh = () => qc.invalidateQueries({ queryKey: ["jwks"] });
  // One action, two faces: with no active signing key it bootstraps the first
  // one ("Generate"); once a key exists it rotates (new active, previous key
  // demoted to rotating). Both call the same backend handler — the label/copy
  // just reflects intent. (rotating keys are verify-only and don't count.)
  const hasActiveKey = keys.some((k) => k.status === "active");

  return (
    <>
      <PageHeader
        title="Signing keys"
        description="EdDSA keys advertised at /.well-known/ouropass/jwks.json."
        action={
          <StepUpDialog
            trigger={
              <Button size="sm">{hasActiveKey ? "Rotate" : "Generate"}</Button>
            }
            title={hasActiveKey ? "Rotate signing key" : "Generate signing key"}
            description={
              hasActiveKey
                ? "Promote a fresh key to active; the previous key keeps verifying until it ages out."
                : "Create the first active signing key (re-sign as owner to confirm)."
            }
            onConfirm={async (su) => {
              await (hasActiveKey ? rotateKey(su) : generateKey(su));
            }}
            onDone={refresh}
          />
        }
      />
      <Card className="mb-4 max-w-lg">
        <CardHeader>
          <CardTitle>Status</CardTitle>
          <CardDescription>
            {keys.length === 0
              ? "No signing key yet — generate one to enable token issuance."
              : `${keys.length} key${keys.length > 1 ? "s" : ""} published in JWKS.`}
          </CardDescription>
        </CardHeader>
        <CardContent>
          <Badge variant={keys.length > 0 ? "success" : "destructive"}>
            {keys.length > 0 ? "JWKS healthy" : "no active key"}
          </Badge>
        </CardContent>
      </Card>
      <QueryState isLoading={q.isLoading} error={q.error} empty={keys.length === 0} emptyText="No keys published.">
        <Table footer={<span>{keys.length} key(s)</span>}>
          <THead>
            <TR>
              <TH>Kid</TH>
              <TH>Status</TH>
              <TH>Type</TH>
              <TH>Curve</TH>
              <TH>Alg</TH>
              <TH className="text-right">Actions</TH>
            </TR>
          </THead>
          <TBody>
            {keys.map((k) => (
              <TR key={k.kid}>
                <TD>
                  <CopyButton value={k.kid} toastLabel="Key id copied" />
                </TD>
                <TD>
                  <StatusBadge
                    status={k.status ?? ""}
                    label={k.status === "active" ? "active · signing" : (k.status ?? "—")}
                  />
                </TD>
                <TD>{k.kty}</TD>
                <TD>{k.crv ?? "—"}</TD>
                <TD>{k.alg ?? "EdDSA"}</TD>
                <TD className="text-right">
                  {k.status === "rotating" && (
                    <StepUpDialog
                      trigger={
                        <Button size="sm" variant="outline">Retire</Button>
                      }
                      title="Retire signing key"
                      description="Remove this verify-only key from the JWKS. Only do this once tokens signed by it have expired — verification of those tokens will then fail."
                      onConfirm={async (su) => {
                        await retireKey(k.kid, su);
                      }}
                      onDone={refresh}
                    />
                  )}
                </TD>
              </TR>
            ))}
          </TBody>
        </Table>
      </QueryState>
    </>
  );
}
