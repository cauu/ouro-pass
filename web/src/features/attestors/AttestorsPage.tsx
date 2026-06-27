import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useForm } from "react-hook-form";
import { createAttestor, deleteAttestor, listAttestors, updateAttestor } from "@/api/admin";
import { ApiError } from "@/api/client";
import { PageHeader, QueryState } from "@/app/page";
import type { Attestor } from "@/lib/types";
import { Badge } from "@/ui/badge";
import { Button } from "@/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/ui/card";
import { ConfirmDialog } from "@/ui/confirm-dialog";
import { Field } from "@/ui/field";
import { Input } from "@/ui/input";
import { Select } from "@/ui/select";
import { Table, TBody, TD, TH, THead, TR } from "@/ui/table";
import { useToast } from "@/ui/toast";

interface AttestorForm {
  label: string;
  poolId: string;
  network: string;
}

const str = (v: unknown) => (typeof v === "string" ? v : "");

// AttestorsPage manages the issuer's on-chain credential sources (S0006): the
// generalization of "the served pool". Each pool_stake attestor attests
// membership in one pool; the thin gate passes a subject holding ANY active
// attestor, and tier_rules evaluate over the aggregate of all of them.
export function AttestorsPage() {
  const { toast } = useToast();
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["attestors"], queryFn: listAttestors });
  const attestors = q.data?.attestors ?? [];

  const { register, handleSubmit, reset } = useForm<AttestorForm>({
    defaultValues: { label: "", poolId: "", network: "mainnet" },
  });

  const invalidate = () => void qc.invalidateQueries({ queryKey: ["attestors"] });
  const onErr = (e: unknown) =>
    toast({
      title: "Action failed",
      description: e instanceof ApiError ? e.message : String(e),
      variant: "destructive",
    });

  const create = useMutation({
    mutationFn: (v: AttestorForm) =>
      createAttestor({
        kind: "pool_stake",
        label: v.label,
        params: { pool_id: v.poolId, network: v.network },
      }),
    onSuccess: () => {
      toast({ title: "Attestor added", variant: "success" });
      reset();
      invalidate();
    },
    onError: onErr,
  });

  const toggle = useMutation({
    mutationFn: (a: Attestor) =>
      updateAttestor(a.attestor_id, { status: a.status === "active" ? "disabled" : "active" }),
    onSuccess: invalidate,
    onError: onErr,
  });

  const remove = useMutation({
    mutationFn: (id: string) => deleteAttestor(id),
    onSuccess: () => {
      toast({ title: "Attestor removed", variant: "success" });
      invalidate();
    },
    onError: onErr,
  });

  return (
    <>
      <PageHeader
        title="Attestors"
        description="On-chain credential sources. A subject holding any active attestor is issued a token; tier_rules evaluate over the aggregate. Only pool_stake is supported today."
      />

      <Card className="mb-4 max-w-lg">
        <CardHeader>
          <CardTitle>Add pool_stake attestor</CardTitle>
          <CardDescription>Attest membership in one stake pool on a network.</CardDescription>
        </CardHeader>
        <CardContent>
          <form className="grid gap-3" onSubmit={handleSubmit((v) => create.mutate(v))}>
            <Field label="Label" hint="This attestor's display name (unique), e.g. members or announcements">
              <Input autoComplete="off" {...register("label", { required: true })} />
            </Field>
            <Field label="Pool ID" hint="bech32 pool id (pool1…)">
              <Input autoComplete="off" {...register("poolId", { required: true })} />
            </Field>
            <Field label="Network">
              <Select {...register("network")}>
                <option value="mainnet">mainnet</option>
                <option value="preprod">preprod</option>
                <option value="preview">preview</option>
              </Select>
            </Field>
            <div>
              <Button type="submit" disabled={create.isPending}>
                Add attestor
              </Button>
            </div>
          </form>
        </CardContent>
      </Card>

      <QueryState isLoading={q.isLoading} error={q.error}>
        <Card>
          <CardHeader>
            <CardTitle>Configured attestors</CardTitle>
            <CardDescription>
              {attestors.length === 0
                ? "None yet — no one can be issued a token until you add an attestor."
                : `${attestors.length} configured.`}
            </CardDescription>
          </CardHeader>
          <CardContent>
            <Table>
              <THead>
                <TR>
                  <TH>Label</TH>
                  <TH>Kind</TH>
                  <TH>Pool / network</TH>
                  <TH>Status</TH>
                  <TH className="text-right">Actions</TH>
                </TR>
              </THead>
              <TBody>
                {attestors.map((a) => (
                  <TR key={a.attestor_id}>
                    <TD>{a.label}</TD>
                    <TD className="font-mono text-xs">{a.kind}</TD>
                    <TD className="font-mono text-xs">
                      {str(a.params.pool_id)}
                      <span className="text-muted-foreground"> · {str(a.params.network) || "—"}</span>
                    </TD>
                    <TD>
                      <Badge variant={a.status === "active" ? "success" : "muted"}>{a.status}</Badge>
                    </TD>
                    <TD className="space-x-2 text-right">
                      <Button variant="ghost" onClick={() => toggle.mutate(a)} disabled={toggle.isPending}>
                        {a.status === "active" ? "Disable" : "Enable"}
                      </Button>
                      <ConfirmDialog
                        trigger={<Button variant="ghost">Delete</Button>}
                        title={`Remove attestor "${a.label}"?`}
                        description="Subjects relying solely on this attestor will stop being issued tokens. This cannot be undone."
                        confirmLabel="Delete attestor"
                        destructive
                        onConfirm={() => remove.mutateAsync(a.attestor_id)}
                      />
                    </TD>
                  </TR>
                ))}
              </TBody>
            </Table>
          </CardContent>
        </Card>
      </QueryState>
    </>
  );
}
