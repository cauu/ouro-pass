import { useState } from "react";
import { Navigate } from "react-router-dom";
import { ApiError } from "@/api/client";
import { useAuth } from "@/auth/AuthContext";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/ui/card";
import { Spinner } from "@/ui/spinner";
import { useToast } from "@/ui/toast";
import { WalletPicker } from "./WalletPicker";

export function LoginPage() {
  const { me, loading, login } = useAuth();
  const [busy, setBusy] = useState<string | null>(null);
  const { toast } = useToast();

  if (loading) {
    return (
      <div className="grid min-h-dvh place-items-center">
        <Spinner className="h-6 w-6" />
      </div>
    );
  }
  if (me) return <Navigate to="/dashboard" replace />;

  async function pick(key: string) {
    setBusy(key);
    try {
      await login(key);
    } catch (e) {
      const msg =
        e instanceof ApiError ? e.message : e instanceof Error ? e.message : "login failed";
      toast({ title: "Login failed", description: msg, variant: "destructive" });
    } finally {
      setBusy(null);
    }
  }

  return (
    <div className="grid min-h-dvh place-items-center p-4">
      <div className="w-full max-w-sm">
        <div className="mb-6 flex items-center gap-3">
          <div className="grid h-9 w-9 place-items-center rounded-lg bg-primary text-sm font-bold text-primary-foreground">
            OP
          </div>
          <div>
            <div className="font-semibold leading-tight">Ouro Pass Admin</div>
            <div className="text-xs text-muted-foreground">Staking identity · operator console</div>
          </div>
        </div>
        <Card>
          <CardHeader>
            <CardTitle className="text-base">Sign in</CardTitle>
            <CardDescription>
              Sign a one-time nonce with your pool owner wallet. Only configured owner keys are
              admitted; operators and viewers are added later by an owner.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <WalletPicker onPick={pick} busy={busy} />
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
