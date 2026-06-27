import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronRight } from "lucide-react";
import { useState } from "react";
import { useForm } from "react-hook-form";
import { listClients, regenerateClientSecret, registerClient } from "@/api/admin";
import { ApiError } from "@/api/client";
import { PageHeader, QueryState } from "@/app/page";
import { useStepUp } from "@/auth/useStepUp";
import { StepUpDialog } from "@/features/auth/StepUpDialog";
import { WalletPicker } from "@/features/auth/WalletPicker";
import { fmtTime } from "@/lib/format";
import type { ClientRegister, OAuthClient } from "@/lib/types";
import { Badge } from "@/ui/badge";
import { Button } from "@/ui/button";
import { CopyButton } from "@/ui/copy-button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/ui/dialog";
import {
  Drawer,
  DrawerContent,
  DrawerDescription,
  DrawerFooter,
  DrawerHeader,
  DrawerTitle,
} from "@/ui/drawer";
import { Field } from "@/ui/field";
import { Input } from "@/ui/input";
import { Select } from "@/ui/select";
import { StatusBadge } from "@/ui/status-badge";
import { Table, TBody, TD, TH, THead, TR } from "@/ui/table";
import { useToast } from "@/ui/toast";

interface ClientForm {
  name: string;
  client_type: "public" | "confidential";
  redirect_uris: string;
  allowed_audiences: string;
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
                <Field label="Name" required>
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
              <p className="text-xs text-muted-foreground">
                PKCE is required for all clients.
              </p>
            </form>
            <p className="text-sm font-medium">Sign &amp; register with your owner wallet:</p>
            <WalletPicker onPick={(k) => void signAndRegister(k)} busy={busy} />
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}

// RegenerateSecretAction issues a fresh secret for a confidential client (step-up)
// and reveals it once — secrets are stored hashed, so the old one is gone.
function RegenerateSecretAction({ clientId, onDone }: { clientId: string; onDone: () => void }) {
  const [secret, setSecret] = useState<string | null>(null);
  return (
    <>
      <StepUpDialog
        trigger={<Button size="sm" variant="outline">Regenerate secret</Button>}
        title="Regenerate client secret"
        description="Issues a new secret and immediately invalidates the current one. The new secret is shown only once — update the client before closing."
        onConfirm={async (su) => {
          const res = await regenerateClientSecret(clientId, su);
          setSecret(res.client_secret);
        }}
        onDone={onDone}
      />
      <Dialog open={!!secret} onOpenChange={(o) => !o && setSecret(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>New client secret</DialogTitle>
            <DialogDescription>
              Copy it now — it is shown only once and cannot be retrieved later.
            </DialogDescription>
          </DialogHeader>
          <code className="block break-all rounded-md border bg-muted p-3 text-sm">{secret}</code>
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={() => secret && navigator.clipboard?.writeText(secret)}>
              Copy
            </Button>
            <Button onClick={() => setSecret(null)}>Done</Button>
          </div>
        </DialogContent>
      </Dialog>
    </>
  );
}

// DetailRow is one label/value line in the client detail drawer.
function DetailRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="grid gap-1">
      <dt className="text-xs font-medium uppercase tracking-wider text-muted-foreground">{label}</dt>
      <dd className="text-sm">{children}</dd>
    </div>
  );
}

// ClientDetailDrawer surfaces fields the list omits (S0008 p2-2): redirect URIs,
// audiences, created time, secret status — all already in the list response — plus
// the regenerate-secret action for confidential clients.
function ClientDetailDrawer({
  client,
  onOpenChange,
  onChanged,
}: {
  client: OAuthClient | null;
  onOpenChange: (open: boolean) => void;
  onChanged: () => void;
}) {
  const list = (xs: string[] | null) =>
    xs && xs.length > 0 ? (
      <ul className="grid gap-1">
        {xs.map((x) => (
          <li key={x} className="break-all font-mono text-xs">
            {x}
          </li>
        ))}
      </ul>
    ) : (
      <span className="text-muted-foreground">—</span>
    );

  return (
    <Drawer open={!!client} onOpenChange={onOpenChange}>
      <DrawerContent>
        {client ? (
          <>
            <DrawerHeader>
              <DrawerTitle>{client.Name}</DrawerTitle>
              <DrawerDescription>OAuth client detail</DrawerDescription>
            </DrawerHeader>
            <dl className="grid gap-4">
              <DetailRow label="Client ID">
                <CopyButton value={client.ClientID} toastLabel="Client ID copied" />
              </DetailRow>
              <DetailRow label="Type">
                <Badge variant={client.ClientType === "confidential" ? "default" : "outline"}>
                  {client.ClientType}
                </Badge>
              </DetailRow>
              <DetailRow label="Status">
                <StatusBadge status={client.Status} />
              </DetailRow>
              <DetailRow label="Client secret">
                {client.ClientSecretHash ? "Set (stored hashed)" : "None (public client)"}
              </DetailRow>
              <DetailRow label="Redirect URIs">{list(client.RedirectURIs)}</DetailRow>
              <DetailRow label="Audiences">{list(client.AllowedAudiences)}</DetailRow>
              <DetailRow label="Created">{fmtTime(client.CreatedAt)}</DetailRow>
            </dl>
            {client.ClientType === "confidential" && (
              <DrawerFooter>
                <RegenerateSecretAction clientId={client.ClientID} onDone={onChanged} />
              </DrawerFooter>
            )}
          </>
        ) : null}
      </DrawerContent>
    </Drawer>
  );
}

export function ClientsPage() {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["clients"], queryFn: listClients });
  const clients = q.data?.clients ?? [];
  const refresh = () => qc.invalidateQueries({ queryKey: ["clients"] });
  const [selected, setSelected] = useState<OAuthClient | null>(null);

  return (
    <>
      <PageHeader
        title="OAuth Clients"
        description="Applications that may request staking-identity logins. Select a client to see its redirect URIs, audiences and secret status."
        action={<RegisterClientDialog onRegistered={refresh} />}
      />
      <QueryState isLoading={q.isLoading} error={q.error} empty={clients.length === 0} emptyText="No clients yet.">
        <Table footer={<span>{clients.length} client(s)</span>}>
          <THead>
            <TR>
              <TH>Client</TH>
              <TH>Type</TH>
              <TH>Audiences</TH>
              <TH>Status</TH>
              <TH className="w-8" />
            </TR>
          </THead>
          <TBody>
            {clients.map((c) => (
              <TR
                key={c.ClientID}
                className="cursor-pointer"
                onClick={() => setSelected(c)}
              >
                <TD>
                  <div className="font-medium">{c.Name}</div>
                  <div className="font-mono text-xs text-muted-foreground">{c.ClientID}</div>
                </TD>
                <TD>
                  <Badge variant={c.ClientType === "confidential" ? "default" : "outline"}>
                    {c.ClientType}
                  </Badge>
                </TD>
                <TD className="text-xs text-muted-foreground">{(c.AllowedAudiences ?? []).join(", ") || "—"}</TD>
                <TD>
                  <StatusBadge status={c.Status} />
                </TD>
                <TD className="text-right text-muted-foreground">
                  <ChevronRight className="h-4 w-4" />
                </TD>
              </TR>
            ))}
          </TBody>
        </Table>
      </QueryState>

      <ClientDetailDrawer
        client={selected}
        onOpenChange={(o) => {
          if (!o) setSelected(null);
        }}
        onChanged={refresh}
      />
    </>
  );
}
