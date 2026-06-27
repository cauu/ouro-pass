import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useForm } from "react-hook-form";
import { createAttestor, deleteAttestor, listAttestors, updateAttestor } from "@/api/admin";
import { ApiError } from "@/api/client";
import { QueryState } from "@/app/page";
import type { Attestor } from "@/lib/types";
import { Button } from "@/ui/button";
import { ConfirmDialog } from "@/ui/confirm-dialog";
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
import { StatusBadge } from "@/ui/status-badge";
import { Table, TBody, TD, TH, THead, TR } from "@/ui/table";
import { useToast } from "@/ui/toast";

interface AttestorForm {
  label: string;
  poolId: string;
  network: string;
}

const str = (v: unknown) => (typeof v === "string" ? v : "");

// CreateAttestorDialog hosts the "add attestor" form in a modal (S0008 p1-5): the
// source list is the section subject, adding a source is an occasional action.
function CreateAttestorDialog({ onCreated }: { onCreated: () => void }) {
  const [open, setOpen] = useState(false);
  const { toast } = useToast();
  const {
    register,
    handleSubmit,
    reset,
    formState: { errors },
  } = useForm<AttestorForm>({
    defaultValues: { label: "", poolId: "", network: "mainnet" },
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
      setOpen(false);
      onCreated();
    },
    onError: (e) =>
      toast({
        title: "Action failed",
        description: e instanceof ApiError ? e.message : String(e),
        variant: "destructive",
      }),
  });

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="sm">Add attestor</Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Add pool_stake attestor</DialogTitle>
          <DialogDescription>Attest membership in one stake pool on a network.</DialogDescription>
        </DialogHeader>
        <form className="grid gap-3" onSubmit={handleSubmit((v) => create.mutate(v))}>
          <Field
            label="Label"
            required
            hint="This attestor's display name (unique), e.g. members or announcements"
            error={errors.label && "Label is required"}
          >
            <Input autoComplete="off" {...register("label", { required: true })} />
          </Field>
          <Field
            label="Pool ID"
            required
            hint="bech32 pool id (pool1…)"
            error={errors.poolId && "Pool ID is required"}
          >
            <Input autoComplete="off" {...register("poolId", { required: true })} />
          </Field>
          <Field label="Network">
            <Select {...register("network")}>
              <option value="mainnet">mainnet</option>
              <option value="preprod">preprod</option>
              <option value="preview">preview</option>
            </Select>
          </Field>
          <DialogFooter>
            <Button type="submit" disabled={create.isPending}>
              Add attestor
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

// SourcesSection is the "Sources" half of Eligibility (S0008): the issuer's
// on-chain credential sources (S0006). Each pool_stake attestor attests
// membership in one pool; the thin gate passes a subject holding ANY active
// attestor, and tier_rules (the sibling section) evaluate over the aggregate.
export function SourcesSection() {
  const { toast } = useToast();
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["attestors"], queryFn: listAttestors });
  const attestors = q.data?.attestors ?? [];

  const invalidate = () => void qc.invalidateQueries({ queryKey: ["attestors"] });
  const onErr = (e: unknown) =>
    toast({
      title: "Action failed",
      description: e instanceof ApiError ? e.message : String(e),
      variant: "destructive",
    });

  const toggle = useMutation({
    mutationFn: (a: Attestor) =>
      updateAttestor(a.attestor_id, { status: a.status === "active" ? "disabled" : "active" }),
    onSuccess: invalidate,
    onError: onErr,
  });

  // remove is only invoked through ConfirmDialog, which surfaces failures itself —
  // no onError here, or the dialog's catch double-toasts.
  const remove = useMutation({
    mutationFn: (id: string) => deleteAttestor(id),
    onSuccess: () => {
      toast({ title: "Attestor removed", variant: "success" });
      invalidate();
    },
  });

  return (
    <>
      <div className="mb-4 flex items-start justify-between gap-4">
        <p className="max-w-2xl text-sm text-muted-foreground">
          On-chain credential sources. A subject holding any active attestor is issued a token; tier
          rules evaluate over the aggregate. Only pool_stake is supported today.
        </p>
        <CreateAttestorDialog onCreated={invalidate} />
      </div>

      <QueryState
        isLoading={q.isLoading}
        error={q.error}
        empty={attestors.length === 0}
        emptyTitle="No attestors yet"
        emptyText="Add a pool_stake attestor — no one can be issued a token until at least one is configured."
      >
        <Table footer={<span>{attestors.length} attestor(s)</span>}>
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
                <TD className="font-medium">{a.label}</TD>
                <TD className="font-mono text-xs">{a.kind}</TD>
                <TD className="font-mono text-xs">
                  {str(a.params.pool_id)}
                  <span className="text-muted-foreground"> · {str(a.params.network) || "—"}</span>
                </TD>
                <TD>
                  <StatusBadge status={a.status} />
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
      </QueryState>
    </>
  );
}
