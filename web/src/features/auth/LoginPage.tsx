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
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle>Ouro Pass Admin</CardTitle>
          <CardDescription>
            Sign in with your pool owner wallet. Only configured owner keys are admitted.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <WalletPicker onPick={pick} busy={busy} />
        </CardContent>
      </Card>
    </div>
  );
}
