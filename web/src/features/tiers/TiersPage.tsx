import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowDown, ArrowUp, Plus, X } from "lucide-react";
import { useEffect, useState } from "react";
import { getPool, listAttestors, setTierRules } from "@/api/admin";
import { ApiError } from "@/api/client";
import { PageHeader, QueryState } from "@/app/page";
import type { Attestor, TierCondition, TierRule } from "@/lib/types";
import { Badge } from "@/ui/badge";
import { Button } from "@/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/ui/card";
import { Input } from "@/ui/input";
import { Select } from "@/ui/select";
import { Table, TBody, TD, TH, THead, TR } from "@/ui/table";
import { Textarea } from "@/ui/textarea";
import { useToast } from "@/ui/toast";

const NUM_OPS = ["==", "!=", ">=", ">", "<=", "<"];
const EQ_OPS = ["==", "!="];
const STATIC_FACTS = ["any_active", "any_held", "total_active_stake"];
const STATE_VALUES = ["active", "pending", "none"];

type FactType = "bool" | "state" | "ada" | "number" | "text";

// factType decides which value editor + which operators a fact gets, so the value
// component matches the fact (boolean → true/false, state → enum, stake → ADA,
// counts → number).
function factType(fact: string): FactType {
  if (fact === "any_active" || fact === "any_held") return "bool";
  if (fact === "total_active_stake" || fact.endsWith(".active_stake_lovelace")) return "ada";
  if (fact.endsWith(".state")) return "state";
  if (fact.endsWith(".epochs_active") || fact.endsWith(".count")) return "number";
  return "text";
}
function opsFor(t: FactType): string[] {
  return t === "bool" || t === "state" ? EQ_OPS : NUM_OPS;
}
function defaultValueFor(t: FactType): string {
  if (t === "bool") return "true";
  if (t === "state") return "active";
  return "";
}

// ADA ⇄ lovelace at the UI/DSL boundary: the rules store lovelace (the on-chain
// unit), but operators configure thresholds in ADA. Exact via BigInt.
const LOVELACE = 1_000_000n;
function adaToLovelace(ada: string): string {
  const s = ada.trim();
  if (s === "") return "";
  if (!/^\d+(\.\d{1,6})?$/.test(s)) return s; // leave malformed input as-is
  const [int, frac = ""] = s.split(".");
  return (BigInt(int) * LOVELACE + BigInt((frac + "000000").slice(0, 6))).toString();
}
function lovelaceToAda(lov: string): string {
  const s = lov.trim();
  if (s === "" || !/^\d+$/.test(s)) return s;
  const v = BigInt(s);
  const frac = (v % LOVELACE).toString().padStart(6, "0").replace(/0+$/, "");
  return frac ? `${v / LOVELACE}.${frac}` : (v / LOVELACE).toString();
}

// describe renders a condition as a human-readable line for the read-only summary
// (stake shown in ADA). Mirrors the DSL's all/any/not + leaf shapes.
function describe(when?: TierCondition, top = true): string {
  if (!when || Object.keys(when).length === 0) return "always";
  if (when.all) {
    const s = when.all.map((c) => describe(c, false)).join(" AND ");
    return top ? s : `(${s})`;
  }
  if (when.any) {
    const s = when.any.map((c) => describe(c, false)).join(" OR ");
    return top ? s : `(${s})`;
  }
  if (when.not) return `NOT (${describe(when.not, false)})`;
  if (when.fact) {
    const v = factType(when.fact) === "ada" ? `${lovelaceToAda(when.value ?? "")} ADA` : when.value ?? "";
    return `${when.fact} ${when.op ?? "=="} ${v}`;
  }
  return JSON.stringify(when);
}

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
  const leaf = (c: TierCondition): Leaf | null => {
    if (!(c.fact && !c.all && !c.any && !c.not)) return null;
    const value = c.value ?? "";
    return { fact: c.fact, op: c.op ?? "==", value: factType(c.fact) === "ada" ? lovelaceToAda(value) : value };
  };
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
  const ls = leaves.map((l) => ({
    fact: l.fact,
    op: l.op,
    value: factType(l.fact) === "ada" ? adaToLovelace(l.value) : l.value,
  }));
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
  const patchLeaf = (i: number, j: number, partial: Partial<Leaf>) =>
    update(i, (x) => ({ ...x, leaves: x.leaves.map((y, yj) => (yj === j ? { ...y, ...partial } : y)) }));
  // Changing the fact re-types the row: reset value to the new type's default and
  // drop an operator the new type doesn't allow.
  const setFact = (i: number, j: number, fact: string) =>
    update(i, (x) => ({
      ...x,
      leaves: x.leaves.map((y, yj) => {
        if (yj !== j) return y;
        const t = factType(fact);
        return { fact, op: opsFor(t).includes(y.op) ? y.op : "==", value: defaultValueFor(t) };
      }),
    }));
  const removeLeaf = (i: number, j: number) =>
    update(i, (x) => ({ ...x, leaves: x.leaves.filter((_, yj) => yj !== j) }));

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
        <Card className="mb-4">
          <CardHeader>
            <CardTitle>Configured tiers</CardTitle>
            <CardDescription>
              {(pool.data?.tier_rules ?? []).length === 0
                ? "No tiers configured — tokens carry no tier opinion until you add a rule below."
                : `${(pool.data?.tier_rules ?? []).length} rule(s), evaluated top-down (first match wins).`}
            </CardDescription>
          </CardHeader>
          <CardContent>
            <Table>
              <THead>
                <TR>
                  <TH>#</TH>
                  <TH>Tier</TH>
                  <TH>Condition</TH>
                </TR>
              </THead>
              <TBody>
                {(pool.data?.tier_rules ?? []).map((r, i) => (
                  <TR key={`${r.tier}-${i}`}>
                    <TD className="text-muted-foreground">{i + 1}</TD>
                    <TD>
                      <Badge variant="success">{r.tier}</Badge>
                    </TD>
                    <TD className="font-mono text-xs">{describe(r.when)}</TD>
                  </TR>
                ))}
              </TBody>
            </Table>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <div className="flex items-center justify-between">
              <CardTitle>Edit rules</CardTitle>
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
                        <Button
                          variant="ghost"
                          size="icon"
                          className="h-7 w-7"
                          title="Move up"
                          onClick={() => move(i, -1)}
                          disabled={i === 0}
                        >
                          <ArrowUp className="h-3.5 w-3.5" />
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon"
                          className="h-7 w-7"
                          title="Move down"
                          onClick={() => move(i, 1)}
                          disabled={i === rules.length - 1}
                        >
                          <ArrowDown className="h-3.5 w-3.5" />
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
                        {r.leaves.map((l, j) => {
                          const t = factType(l.fact);
                          return (
                            <div key={j} className="flex items-center gap-2">
                              <Select
                                value={facts.includes(l.fact) ? l.fact : "__custom"}
                                onChange={(e) => {
                                  if (e.target.value !== "__custom") setFact(i, j, e.target.value);
                                }}
                              >
                                {facts.map((f) => (
                                  <option key={f} value={f}>
                                    {f}
                                  </option>
                                ))}
                                {!facts.includes(l.fact) && (
                                  <option value="__custom">{l.fact || "(custom)"}</option>
                                )}
                              </Select>
                              <Select
                                className="max-w-[80px]"
                                value={l.op}
                                onChange={(e) => patchLeaf(i, j, { op: e.target.value })}
                              >
                                {opsFor(t).map((o) => (
                                  <option key={o} value={o}>
                                    {o}
                                  </option>
                                ))}
                              </Select>
                              {t === "bool" ? (
                                <Select value={l.value || "true"} onChange={(e) => patchLeaf(i, j, { value: e.target.value })}>
                                  <option value="true">true</option>
                                  <option value="false">false</option>
                                </Select>
                              ) : t === "state" ? (
                                <Select value={l.value || "active"} onChange={(e) => patchLeaf(i, j, { value: e.target.value })}>
                                  {STATE_VALUES.map((s) => (
                                    <option key={s} value={s}>
                                      {s}
                                    </option>
                                  ))}
                                  {l.value && !STATE_VALUES.includes(l.value) && (
                                    <option value={l.value}>{l.value}</option>
                                  )}
                                </Select>
                              ) : (
                                <div className="flex items-center gap-1">
                                  <Input
                                    className="max-w-[200px]"
                                    inputMode={t === "ada" || t === "number" ? "numeric" : undefined}
                                    placeholder={
                                      t === "ada" ? "ADA, e.g. 100000" : t === "number" ? "e.g. 3" : "value"
                                    }
                                    value={l.value}
                                    onChange={(e) => patchLeaf(i, j, { value: e.target.value })}
                                  />
                                  {t === "ada" && <span className="text-xs text-muted-foreground">ADA</span>}
                                </div>
                              )}
                              <Button
                                variant="ghost"
                                size="icon"
                                className="h-7 w-7 shrink-0"
                                title="Remove condition"
                                onClick={() => removeLeaf(i, j)}
                              >
                                <X className="h-3.5 w-3.5" />
                              </Button>
                            </div>
                          );
                        })}
                        <div>
                          <Button
                            variant="ghost"
                            size="sm"
                            onClick={() => {
                              const f0 = facts[0] ?? "any_active";
                              update(i, (x) => ({
                                ...x,
                                leaves: [...x.leaves, { fact: f0, op: "==", value: defaultValueFor(factType(f0)) }],
                              }));
                            }}
                          >
                            <Plus className="h-3.5 w-3.5" />
                            Add condition
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
                    <Plus className="h-3.5 w-3.5" />
                    Add rule
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
