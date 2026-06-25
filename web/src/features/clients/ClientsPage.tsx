import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useForm } from "react-hook-form";
import { listClients, registerClient } from "@/api/admin";
import { ApiError } from "@/api/client";
import { PageHeader, QueryState } from "@/app/page";
import { useStepUp } from "@/auth/useStepUp";
import { WalletPicker } from "@/features/auth/WalletPicker";
import type { ClientRegister } from "@/lib/types";
import { Badge } from "@/ui/badge";
import { Button } from "@/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/ui/dialog";
import { Field } from "@/ui/field";
import { Input } from "@/ui/input";
import { Select } from "@/ui/select";
import { Table, TBody, TD, TH, THead, TR } from "@/ui/table";
import { useToast } from "@/ui/toast";

interface ClientForm {
  name: string;
  client_type: "public" | "confidential";
  redirect_uris: string;
  allowed_audiences: string;
  pkce_required: boolean;
}

const splitLines = (s: string) =>
  s
    .split(/[\n,]/)
    .map((x) => x.trim())
    .filter(Boolean);

function RegisterClientDialog({ onRegistered }: { onRegistered: () => void }) {
  const [open, setOpen] = useState(false);
  // After registration the server returns the generated client_id (always) and a
  // one-time client_secret (confidential only) — both shown once here.
  const [result, setResult] = useState<{ clientId: string; secret?: string } | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const runStepUp = useStepUp();
  const { toast } = useToast();
  const { register, getValues, reset } = useForm<ClientForm>({
    defaultValues: {
      name: "",
      client_type: "confidential",
      redirect_uris: "",
      allowed_audiences: "",
      pkce_required: false,
    },
  });

  function close() {
    setOpen(false);
    setResult(null);
    setBusy(null);
    reset();
  }

  // Registering issues credentials, so it needs a fresh step-up: pick wallet ->
  // sign -> POST with the form body. confidential clients get a one-time secret.
  async function signAndRegister(walletKey: string) {
    const v = getValues();
    if (!v.name) {
      toast({ title: "name is required", variant: "destructive" });
      return;
    }
    setBusy(walletKey);
    try {
      const stepUp = await runStepUp(walletKey);
      const body: ClientRegister = {
        name: v.name,
        client_type: v.client_type,
        redirect_uris: splitLines(v.redirect_uris),
        allowed_audiences: splitLines(v.allowed_audiences),
        pkce_required: v.pkce_required,
      };
      const res = await registerClient({ ...body, ...stepUp });
      onRegistered();
      setResult({ clientId: res.client_id, secret: res.client_secret });
    } catch (e) {
      toast({
        title: "Register failed",
        description: e instanceof ApiError ? e.message : String(e),
        variant: "destructive",
      });
    } finally {
      setBusy(null);
    }
  }

  return (
    <Dialog open={open} onOpenChange={(o) => (o ? setOpen(true) : close())}>
      <DialogTrigger asChild>
        <Button size="sm">Register client</Button>
      </DialogTrigger>
      <DialogContent>
        {result ? (
          <>
            <DialogHeader>
              <DialogTitle>Client registered</DialogTitle>
              <DialogDescription>
                {result.secret
                  ? "Save the client ID and secret now — the secret is shown only once and cannot be retrieved later."
                  : "Save the client ID — your application uses it to identify itself."}
              </DialogDescription>
            </DialogHeader>
            <Field label="Client ID">
              <code className="block break-all rounded-md border bg-muted p-3 text-sm">{result.clientId}</code>
            </Field>
            {result.secret && (
              <Field label="Client secret">
                <code className="block break-all rounded-md border bg-muted p-3 text-sm">{result.secret}</code>
              </Field>
            )}
            <div className="flex justify-end gap-2">
              <Button
                variant="outline"
                onClick={() =>
                  navigator.clipboard?.writeText(
                    result.secret ? `${result.clientId}\n${result.secret}` : result.clientId,
                  )
                }
              >
                Copy
              </Button>
              <Button onClick={close}>Done</Button>
            </div>
          </>
        ) : (
          <>
            <DialogHeader>
              <DialogTitle>Register OAuth client</DialogTitle>
              <DialogDescription>
                Requires a fresh owner step-up signature. Confidential clients receive a one-time secret.
              </DialogDescription>
            </DialogHeader>
            <form className="grid gap-3">
              <div className="grid grid-cols-2 gap-3">
                <Field label="Name">
                  <Input {...register("name")} placeholder="Web App" />
                </Field>
                <Field label="Type">
                  <Select {...register("client_type")}>
                    <option value="confidential">confidential</option>
                    <option value="public">public</option>
                  </Select>
                </Field>
              </div>
              <Field label="Redirect URIs (one per line)">
                <Input {...register("redirect_uris")} placeholder="https://app/cb" />
              </Field>
              <Field label="Audiences (comma-sep)">
                <Input {...register("allowed_audiences")} placeholder="app:ouro" />
              </Field>
              <label className="flex items-center gap-2 text-sm">
                <input type="checkbox" {...register("pkce_required")} /> Require PKCE
              </label>
            </form>
            <p className="text-sm font-medium">Sign &amp; register with your owner wallet:</p>
            <WalletPicker onPick={(k) => void signAndRegister(k)} busy={busy} />
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}

export function ClientsPage() {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["clients"], queryFn: listClients });
  const clients = q.data?.clients ?? [];

  return (
    <>
      <PageHeader
        title="OAuth Clients"
        description="Applications that may request staking-identity logins."
        action={<RegisterClientDialog onRegistered={() => qc.invalidateQueries({ queryKey: ["clients"] })} />}
      />
      <QueryState isLoading={q.isLoading} error={q.error} empty={clients.length === 0} emptyText="No clients yet.">
        <Table>
          <THead>
            <TR>
              <TH>Client</TH>
              <TH>Type</TH>
              <TH>Audiences</TH>
              <TH>Status</TH>
            </TR>
          </THead>
          <TBody>
            {clients.map((c) => (
              <TR key={c.ClientID}>
                <TD>
                  <div className="font-medium">{c.Name}</div>
                  <div className="font-mono text-xs text-muted-foreground">{c.ClientID}</div>
                </TD>
                <TD>{c.ClientType}</TD>
                <TD className="text-xs text-muted-foreground">{(c.AllowedAudiences ?? []).join(", ") || "—"}</TD>
                <TD>
                  <Badge variant={c.Status === "active" ? "success" : "muted"}>{c.Status}</Badge>
                </TD>
              </TR>
            ))}
          </TBody>
        </Table>
      </QueryState>
    </>
  );
}
