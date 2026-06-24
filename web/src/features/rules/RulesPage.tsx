import { zodResolver } from "@hookform/resolvers/zod";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useForm } from "react-hook-form";
import { z } from "zod";
import { listRules, upsertRule } from "@/api/admin";
import { ApiError } from "@/api/client";
import { PageHeader, QueryState } from "@/app/page";
import type { Rule } from "@/lib/types";
import { Badge } from "@/ui/badge";
import { Button } from "@/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/ui/dialog";
import { Field } from "@/ui/field";
import { Input } from "@/ui/input";
import { Select } from "@/ui/select";
import { Table, TBody, TD, TH, THead, TR } from "@/ui/table";
import { Textarea } from "@/ui/textarea";
import { useToast } from "@/ui/toast";

const schema = z.object({
  rule_id: z.string().min(1, "required"),
  name: z.string().min(1, "required"),
  tier: z.string().min(1, "required"),
  priority: z.coerce.number().int(),
  status: z.enum(["active", "disabled"]),
  entitlements: z.string(),
  rule_config: z.string().refine((s) => {
    try {
      JSON.parse(s || "{}");
      return true;
    } catch {
      return false;
    }
  }, "must be valid JSON"),
});
type FormValues = z.input<typeof schema>;

function RuleDialog({ initial, onSaved }: { initial?: Rule; onSaved: () => void }) {
  const [open, setOpen] = useState(false);
  const { toast } = useToast();
  const {
    register,
    handleSubmit,
    formState: { errors },
  } = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: {
      rule_id: initial?.RuleID ?? "",
      name: initial?.Name ?? "",
      tier: initial?.Tier ?? "gold",
      priority: initial?.Priority ?? 10,
      status: (initial?.Status as "active" | "disabled") ?? "active",
      entitlements: (initial?.Entitlements ?? []).join(", "),
      rule_config: initial?.RuleConfig
        ? JSON.stringify(initial.RuleConfig, null, 2)
        : '{\n  "min_active_stake_lovelace": "1000000"\n}',
    },
  });

  const save = useMutation({
    mutationFn: (v: FormValues) =>
      upsertRule({
        rule_id: v.rule_id,
        name: v.name,
        tier: v.tier,
        priority: Number(v.priority),
        status: v.status,
        entitlements: v.entitlements
          .split(",")
          .map((e) => e.trim())
          .filter(Boolean),
        rule_config: JSON.parse(v.rule_config || "{}"),
      }),
    onSuccess: () => {
      toast({ title: "Rule saved", variant: "success" });
      setOpen(false);
      onSaved();
    },
    onError: (e) =>
      toast({
        title: "Save failed",
        description: e instanceof ApiError ? e.message : String(e),
        variant: "destructive",
      }),
  });

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="sm" variant={initial ? "outline" : "default"}>
          {initial ? "Edit" : "New rule"}
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{initial ? "Edit rule" : "New membership rule"}</DialogTitle>
          <DialogDescription>
            Rules map staking eligibility to a tier + entitlements (highest priority wins).
          </DialogDescription>
        </DialogHeader>
        <form className="grid gap-3" onSubmit={handleSubmit((v) => save.mutate(v))}>
          <div className="grid grid-cols-2 gap-3">
            <Field label="Rule ID" error={errors.rule_id?.message}>
              <Input {...register("rule_id")} disabled={!!initial} placeholder="gold" />
            </Field>
            <Field label="Name" error={errors.name?.message}>
              <Input {...register("name")} placeholder="Gold tier" />
            </Field>
            <Field label="Tier" error={errors.tier?.message}>
              <Input {...register("tier")} placeholder="gold" />
            </Field>
            <Field label="Priority" error={errors.priority?.message}>
              <Input type="number" {...register("priority")} />
            </Field>
            <Field label="Status" error={errors.status?.message}>
              <Select {...register("status")}>
                <option value="active">active</option>
                <option value="disabled">disabled</option>
              </Select>
            </Field>
            <Field label="Entitlements (comma-sep)" error={errors.entitlements?.message}>
              <Input {...register("entitlements")} placeholder="read, vip" />
            </Field>
          </div>
          <Field label="Rule config (JSON)" error={errors.rule_config?.message}>
            <Textarea rows={6} {...register("rule_config")} />
          </Field>
          <DialogFooter>
            <Button type="submit" disabled={save.isPending}>
              Save
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function RulesPage() {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["rules"], queryFn: listRules });
  const rules = q.data?.rules ?? [];
  const invalidate = () => qc.invalidateQueries({ queryKey: ["rules"] });

  return (
    <>
      <PageHeader
        title="Rules"
        description="Membership eligibility rules."
        action={<RuleDialog onSaved={invalidate} />}
      />
      <QueryState isLoading={q.isLoading} error={q.error} empty={rules.length === 0} emptyText="No rules yet.">
        <Table>
          <THead>
            <TR>
              <TH>Rule</TH>
              <TH>Tier</TH>
              <TH>Priority</TH>
              <TH>Status</TH>
              <TH>Entitlements</TH>
              <TH className="text-right">Actions</TH>
            </TR>
          </THead>
          <TBody>
            {rules.map((r) => (
              <TR key={r.RuleID}>
                <TD>
                  <div className="font-medium">{r.Name}</div>
                  <div className="font-mono text-xs text-muted-foreground">{r.RuleID}</div>
                </TD>
                <TD>
                  <Badge>{r.Tier}</Badge>
                </TD>
                <TD className="tabular-nums">{r.Priority}</TD>
                <TD>
                  <Badge variant={r.Status === "active" ? "success" : "muted"}>{r.Status}</Badge>
                </TD>
                <TD className="text-xs text-muted-foreground">{(r.Entitlements ?? []).join(", ") || "—"}</TD>
                <TD className="text-right">
                  <RuleDialog initial={r} onSaved={invalidate} />
                </TD>
              </TR>
            ))}
          </TBody>
        </Table>
      </QueryState>
    </>
  );
}
