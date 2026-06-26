import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { getPool, setTierRules } from "@/api/admin";
import { ApiError } from "@/api/client";
import { PageHeader, QueryState } from "@/app/page";
import type { TierRule } from "@/lib/types";
import { Badge } from "@/ui/badge";
import { Button } from "@/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/ui/card";
import { Table, TBody, TD, TH, THead, TR } from "@/ui/table";
import { Textarea } from "@/ui/textarea";
import { useToast } from "@/ui/toast";

const SAMPLE: TierRule[] = [
  { tier: "gold", min_state: "active", min_active_stake: "1000000000000" },
  { tier: "silver", min_state: "active", min_active_stake: "100000000000" },
  { tier: "basic", min_state: "active" },
];

// TiersPage edits the issuer's first-party tier mapping (PoolConfig.tier_rules,
// S0004 §2.6). Rules are ordered, first match wins. This is the issuer's own
// opinion for its channels (Telegram/Push); external relying parties ignore it
// and read the raw token facts.
export function TiersPage() {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["pool"], queryFn: getPool });
  const rules = q.data?.tier_rules ?? [];

  const [draft, setDraft] = useState("");
  const [busy, setBusy] = useState(false);
  const { toast } = useToast();

  // Seed the editor from the server once loaded (and after a save refetch).
  useEffect(() => {
    if (q.data) setDraft(JSON.stringify(q.data.tier_rules ?? [], null, 2));
  }, [q.data]);

  async function save() {
    let parsed: TierRule[];
    try {
      parsed = JSON.parse(draft);
      if (!Array.isArray(parsed)) throw new Error("must be a JSON array");
    } catch (e) {
      toast({ title: "Invalid JSON", description: e instanceof Error ? e.message : String(e), variant: "destructive" });
      return;
    }
    setBusy(true);
    try {
      await setTierRules(parsed);
      await qc.invalidateQueries({ queryKey: ["pool"] });
      toast({ title: "Tier rules saved", variant: "success" });
    } catch (e) {
      toast({
        title: "Save failed",
        description: e instanceof ApiError ? e.message : String(e),
        variant: "destructive",
      });
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <PageHeader
        title="Tiers"
        description="First-party tier mapping (state + active stake → tier). Ordered, first match wins. Used only by the issuer's own channels — external apps read raw token facts."
      />
      <QueryState isLoading={q.isLoading} error={q.error}>
        <Card className="mb-4">
          <CardHeader>
            <CardTitle>Current tiers</CardTitle>
            <CardDescription>
              {rules.length === 0
                ? "No tiers configured — tokens carry no tier opinion until you add rules."
                : `${rules.length} rule${rules.length > 1 ? "s" : ""}, evaluated top-down.`}
            </CardDescription>
          </CardHeader>
          <CardContent>
            <Table>
              <THead>
                <TR>
                  <TH>#</TH>
                  <TH>Tier</TH>
                  <TH>Min state</TH>
                  <TH>Min active stake (lovelace)</TH>
                </TR>
              </THead>
              <TBody>
                {rules.map((r, i) => (
                  <TR key={`${r.tier}-${i}`}>
                    <TD className="text-muted-foreground">{i + 1}</TD>
                    <TD>
                      <Badge variant="success">{r.tier}</Badge>
                    </TD>
                    <TD>{r.min_state ?? "active"}</TD>
                    <TD className="font-mono text-xs">{r.min_active_stake || "—"}</TD>
                  </TR>
                ))}
              </TBody>
            </Table>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Edit rules (JSON)</CardTitle>
            <CardDescription>
              Array of {`{ tier, min_state?, min_active_stake? }`}. min_state is "active" or "pending"
              (default active); min_active_stake is decimal lovelace ("" = no minimum).
            </CardDescription>
          </CardHeader>
          <CardContent className="grid gap-3">
            <Textarea
              className="min-h-[220px] font-mono text-xs"
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              spellCheck={false}
            />
            <div className="flex items-center gap-2">
              <Button onClick={() => void save()} disabled={busy}>
                {busy ? "Saving…" : "Save tiers"}
              </Button>
              <Button
                variant="ghost"
                onClick={() => setDraft(JSON.stringify(SAMPLE, null, 2))}
                disabled={busy}
              >
                Insert example
              </Button>
            </div>
          </CardContent>
        </Card>
      </QueryState>
    </>
  );
}
