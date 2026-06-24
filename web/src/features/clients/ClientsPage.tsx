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
  client_id: string;
  name: string;
  client_type: "public" | "confidential";
  party: string;
  redirect_uris: string;
  allowed_audiences: string;
  allowed_scopes: string;
  pkce_required: boolean;
}

const splitLines = (s: string) =>
  s
    .split(/[\n,]/)
    .map((x) => x.trim())
    .filter(Boolean);

function RegisterClientDialog({ onRegistered }: { onRegistered: () => void }) {
  const [open, setOpen] = useState(false);
  const [secret, setSecret] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const runStepUp = useStepUp();
  const { toast } = useToast();
  const { register, getValues, reset } = useForm<ClientForm>({
    defaultValues: {
      client_id: "",
      name: "",
      client_type: "confidential",
      party: "first_party",
      redirect_uris: "",
      allowed_audiences: "",
      allowed_scopes: "",
      pkce_required: false,
    },
  });

  function close() {
    setOpen(false);
    setSecret(null);
    setBusy(null);
    reset();
  }

  // Registering issues credentials, so it needs a fresh step-up: pick wallet ->
  // sign -> POST with the form body. confidential clients get a one-time secret.
  async function signAndRegister(walletKey: string) {
    const v = getValues();
    if (!v.client_id || !v.name) {
      toast({ title: "client_id and name are required", variant: "destructive" });
      return;
    }
    setBusy(walletKey);
    try {
      const stepUp = await runStepUp(walletKey);
      const body: ClientRegister = {
        client_id: v.client_id,
        name: v.name,
        client_type: v.client_type,
        party: v.party,
        redirect_uris: splitLines(v.redirect_uris),
        allowed_audiences: splitLines(v.allowed_audiences),
        allowed_scopes: splitLines(v.allowed_scopes),
        pkce_required: v.pkce_required,
      };
      const res = await registerClient({ ...body, ...stepUp });
      onRegistered();
      if (res.client_secret) {
        setSecret(res.client_secret);
      } else {
        toast({ title: "Client registered", variant: "success" });
        close();
      }
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
        {secret ? (
          <>
            <DialogHeader>
              <DialogTitle>Client secret</DialogTitle>
              <DialogDescription>
                Copy it now — it is shown only once and cannot be retrieved later.
              </DialogDescription>
            </DialogHeader>
            <code className="block break-all rounded-md border bg-muted p-3 text-sm">{secret}</code>
            <div className="flex justify-end gap-2">
              <Button
                variant="outline"
                onClick={() => navigator.clipboard?.writeText(secret)}
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
                <Field label="Client ID">
                  <Input {...register("client_id")} placeholder="web-app" />
                </Field>
                <Field label="Name">
                  <Input {...register("name")} placeholder="Web App" />
                </Field>
                <Field label="Type">
                  <Select {...register("client_type")}>
                    <option value="confidential">confidential</option>
                    <option value="public">public</option>
                  </Select>
                </Field>
                <Field label="Party">
                  <Select {...register("party")}>
                    <option value="first_party">first_party</option>
                    <option value="third_party">third_party</option>
                  </Select>
                </Field>
              </div>
              <Field label="Redirect URIs (one per line)">
                <Input {...register("redirect_uris")} placeholder="https://app/cb" />
              </Field>
              <div className="grid grid-cols-2 gap-3">
                <Field label="Audiences (comma-sep)">
                  <Input {...register("allowed_audiences")} placeholder="app:ouro" />
                </Field>
                <Field label="Scopes (comma-sep)">
                  <Input {...register("allowed_scopes")} placeholder="read" />
                </Field>
              </div>
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
              <TH>Party</TH>
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
                <TD>{c.Party}</TD>
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
