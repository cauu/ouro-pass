import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { getPool, listAttestors, setTierRules } from "@/api/admin";
import { ApiError } from "@/api/client";
import { PageHeader, QueryState } from "@/app/page";
import type { Attestor, TierCondition, TierRule } from "@/lib/types";
import { Button } from "@/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/ui/card";
import { Input } from "@/ui/input";
import { Select } from "@/ui/select";
import { Textarea } from "@/ui/textarea";
import { useToast } from "@/ui/toast";

const OPS = ["==", "!=", ">=", ">", "<=", "<"];
const STATIC_FACTS = ["any_active", "any_held", "total_active_stake"];

interface Leaf {
  fact: string;
  op: string;
  value: string;
}
// EditRule is the flat, builder-friendly view of a TierRule. advancedWhen holds a
// condition too complex for the builder (nested / not), edited via JSON instead.
interface EditRule {
  tier: string;
  combinator: "all" | "any";
  leaves: Leaf[];
  advancedWhen?: TierCondition;
}

// availableFacts lists the named facts the configured attestor set produces, so the
// fact dropdown is grounded in this deployment's pools (S0006 §2.4).
function availableFacts(attestors: Attestor[]): string[] {
  const perPool: string[] = [];
  for (const a of attestors) {
    const pid = typeof a.params?.pool_id === "string" ? a.params.pool_id : "";
    if (a.kind === "pool_stake" && pid) {
      perPool.push(`pool:${pid}.state`, `pool:${pid}.active_stake_lovelace`, `pool:${pid}.epochs_active`);
    }
  }
  return [...STATIC_FACTS, ...perPool];
}

// flatten reduces a rule's condition to the builder model, or null when it nests
// beyond a flat all/any of leaves (then the rule is edited as JSON).
function flatten(when?: TierCondition): { combinator: "all" | "any"; leaves: Leaf[] } | null {
  if (!when || Object.keys(when).length === 0) return { combinator: "all", leaves: [] };
  const leaf = (c: TierCondition): Leaf | null =>
    c.fact && !c.all && !c.any && !c.not ? { fact: c.fact, op: c.op ?? "==", value: c.value ?? "" } : null;
  const self = leaf(when);
  if (self) return { combinator: "all", leaves: [self] };
  const arr = when.all ?? when.any;
  if (arr && !when.not) {
    const leaves = arr.map(leaf);
    if (leaves.every((l): l is Leaf => l !== null)) {
      return { combinator: when.all ? "all" : "any", leaves };
    }
  }
  return null;
}

function toWhen(combinator: "all" | "any", leaves: Leaf[]): TierCondition | undefined {
  const ls = leaves.map((l) => ({ fact: l.fact, op: l.op, value: l.value }));
  if (ls.length === 0) return undefined; // catch-all
  if (ls.length === 1) return ls[0];
  return combinator === "all" ? { all: ls } : { any: ls };
}

function serialize(rules: EditRule[]): TierRule[] {
  return rules.map((r) =>
    r.advancedWhen ? { tier: r.tier, when: r.advancedWhen } : { tier: r.tier, when: toWhen(r.combinator, r.leaves) },
  );
}

function toEdit(rules: TierRule[]): EditRule[] {
  return rules.map((r) => {
    const flat = flatten(r.when);
    return flat
      ? { tier: r.tier, combinator: flat.combinator, leaves: flat.leaves }
      : { tier: r.tier, combinator: "all", leaves: [], advancedWhen: r.when };
  });
}

// TiersPage edits the issuer's first-party tier mapping (S0006 §2.4): an ordered
// list of rules, each pairing a tier with a boolean condition over the aggregate
// facts the attestor set produces. First match wins; no match → no tier. Used only
// by the issuer's own channels — external relying parties read raw token facts.
export function TiersPage() {
  const qc = useQueryClient();
  const pool = useQuery({ queryKey: ["pool"], queryFn: getPool });
  const attestorsQ = useQuery({ queryKey: ["attestors"], queryFn: listAttestors });
  const facts = availableFacts(attestorsQ.data?.attestors ?? []);
  const { toast } = useToast();

  const [rules, setRules] = useState<EditRule[]>([]);
  const [mode, setMode] = useState<"builder" | "json">("builder");
  const [jsonDraft, setJsonDraft] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (pool.data) setRules(toEdit(pool.data.tier_rules ?? []));
  }, [pool.data]);

  const update = (i: number, fn: (r: EditRule) => EditRule) =>
    setRules((rs) => rs.map((r, idx) => (idx === i ? fn(r) : r)));
  const move = (i: number, d: -1 | 1) =>
    setRules((rs) => {
      const j = i + d;
      if (j < 0 || j >= rs.length) return rs;
      const next = [...rs];
      [next[i], next[j]] = [next[j], next[i]];
      return next;
    });

  async function save() {
    setBusy(true);
    try {
      let out: TierRule[];
      if (mode === "json") {
        out = JSON.parse(jsonDraft);
        if (!Array.isArray(out)) throw new Error("must be a JSON array");
      } else {
        out = serialize(rules);
        if (rules.some((r) => !r.tier.trim())) throw new Error("every rule needs a tier name");
      }
      await setTierRules(out);
      await qc.invalidateQueries({ queryKey: ["pool"] });
      toast({ title: "Tier rules saved", variant: "success" });
    } catch (e) {
      toast({
        title: "Save failed",
        description: e instanceof ApiError ? e.message : e instanceof Error ? e.message : String(e),
        variant: "destructive",
      });
    } finally {
      setBusy(false);
    }
  }

  function switchToJson() {
    setJsonDraft(JSON.stringify(serialize(rules), null, 2));
    setMode("json");
  }
  function switchToBuilder() {
    try {
      const parsed: TierRule[] = JSON.parse(jsonDraft);
      if (!Array.isArray(parsed)) throw new Error("must be a JSON array");
      setRules(toEdit(parsed));
      setMode("builder");
    } catch (e) {
      toast({ title: "Invalid JSON", description: e instanceof Error ? e.message : String(e), variant: "destructive" });
    }
  }

  return (
    <>
      <PageHeader
        title="Tiers"
        description="First-party tier mapping over the aggregate of all configured attestors. Ordered, first match wins; no match → no tier. Used only by the issuer's own channels — external apps read raw token facts."
      />
      <QueryState isLoading={pool.isLoading} error={pool.error}>
        <Card>
          <CardHeader>
            <div className="flex items-center justify-between">
              <CardTitle>Tier rules</CardTitle>
              <Button variant="ghost" size="sm" onClick={mode === "builder" ? switchToJson : switchToBuilder}>
                {mode === "builder" ? "Edit as JSON" : "Back to builder"}
              </Button>
            </div>
            <CardDescription>
              Each rule = a tier + a condition over facts. Facts:{" "}
              <span className="font-mono text-xs">any_active</span>,{" "}
              <span className="font-mono text-xs">total_active_stake</span>, per-pool{" "}
              <span className="font-mono text-xs">pool:&lt;id&gt;.state</span>. JSON mode supports nested
              all/any/not.
            </CardDescription>
          </CardHeader>
          <CardContent className="grid gap-4">
            {mode === "json" ? (
              <Textarea
                className="min-h-[260px] font-mono text-xs"
                value={jsonDraft}
                onChange={(e) => setJsonDraft(e.target.value)}
                spellCheck={false}
              />
            ) : (
              <>
                {rules.length === 0 && (
                  <p className="text-sm text-muted-foreground">
                    No tiers — tokens carry no tier opinion until you add a rule.
                  </p>
                )}
                {rules.map((r, i) => (
                  <div key={i} className="grid gap-3 rounded-md border p-3">
                    <div className="flex items-center gap-2">
                      <span className="text-xs text-muted-foreground">{i + 1}</span>
                      <Input
                        className="max-w-[200px]"
                        placeholder="tier name (e.g. gold)"
                        value={r.tier}
                        onChange={(e) => update(i, (x) => ({ ...x, tier: e.target.value }))}
                      />
                      <div className="ml-auto flex gap-1">
                        <Button variant="ghost" size="sm" onClick={() => move(i, -1)} disabled={i === 0}>
                          ↑
                        </Button>
                        <Button variant="ghost" size="sm" onClick={() => move(i, 1)} disabled={i === rules.length - 1}>
                          ↓
                        </Button>
                        <Button
                          variant="ghost"
                          size="sm"
                          onClick={() => setRules((rs) => rs.filter((_, idx) => idx !== i))}
                        >
                          Remove
                        </Button>
                      </div>
                    </div>

                    {r.advancedWhen ? (
                      <p className="font-mono text-xs text-muted-foreground">
                        advanced condition — edit in JSON mode: {JSON.stringify(r.advancedWhen)}
                      </p>
                    ) : (
                      <>
                        <div className="flex items-center gap-2 text-sm">
                          <span className="text-muted-foreground">
                            {r.leaves.length === 0 ? "Always matches (catch-all)" : "Match"}
                          </span>
                          {r.leaves.length > 1 && (
                            <Select
                              className="max-w-[90px]"
                              value={r.combinator}
                              onChange={(e) =>
                                update(i, (x) => ({ ...x, combinator: e.target.value as "all" | "any" }))
                              }
                            >
                              <option value="all">ALL</option>
                              <option value="any">ANY</option>
                            </Select>
                          )}
                          {r.leaves.length > 1 && <span className="text-muted-foreground">of:</span>}
                        </div>
                        {r.leaves.map((l, j) => (
                          <div key={j} className="flex items-center gap-2">
                            <Select
                              value={facts.includes(l.fact) ? l.fact : "__custom"}
                              onChange={(e) =>
                                update(i, (x) => ({
                                  ...x,
                                  leaves: x.leaves.map((y, yj) =>
                                    yj === j ? { ...y, fact: e.target.value === "__custom" ? y.fact : e.target.value } : y,
                                  ),
                                }))
                              }
                            >
                              {facts.map((f) => (
                                <option key={f} value={f}>
                                  {f}
                                </option>
                              ))}
                              {!facts.includes(l.fact) && <option value="__custom">{l.fact || "(custom)"}</option>}
                            </Select>
                            <Select
                              className="max-w-[80px]"
                              value={l.op}
                              onChange={(e) =>
                                update(i, (x) => ({
                                  ...x,
                                  leaves: x.leaves.map((y, yj) => (yj === j ? { ...y, op: e.target.value } : y)),
                                }))
                              }
                            >
                              {OPS.map((o) => (
                                <option key={o} value={o}>
                                  {o}
                                </option>
                              ))}
                            </Select>
                            <Input
                              className="max-w-[220px]"
                              placeholder="value (e.g. true / 1000000)"
                              value={l.value}
                              onChange={(e) =>
                                update(i, (x) => ({
                                  ...x,
                                  leaves: x.leaves.map((y, yj) => (yj === j ? { ...y, value: e.target.value } : y)),
                                }))
                              }
                            />
                            <Button
                              variant="ghost"
                              size="sm"
                              onClick={() =>
                                update(i, (x) => ({ ...x, leaves: x.leaves.filter((_, yj) => yj !== j) }))
                              }
                            >
                              ✕
                            </Button>
                          </div>
                        ))}
                        <div>
                          <Button
                            variant="ghost"
                            size="sm"
                            onClick={() =>
                              update(i, (x) => ({
                                ...x,
                                leaves: [...x.leaves, { fact: facts[0] ?? "any_active", op: "==", value: "" }],
                              }))
                            }
                          >
                            + Add condition
                          </Button>
                        </div>
                      </>
                    )}
                  </div>
                ))}
                <div>
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => setRules((rs) => [...rs, { tier: "", combinator: "all", leaves: [] }])}
                  >
                    + Add rule
                  </Button>
                </div>
              </>
            )}
            <div>
              <Button onClick={() => void save()} disabled={busy}>
                {busy ? "Saving…" : "Save tiers"}
              </Button>
            </div>
          </CardContent>
        </Card>
      </QueryState>
    </>
  );
}
