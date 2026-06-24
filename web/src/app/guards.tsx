import type { ReactNode } from "react";
import { Navigate } from "react-router-dom";
import { useAuth } from "@/auth/AuthContext";
import { roleRank } from "@/lib/config";
import type { Role } from "@/lib/types";
import { Spinner } from "@/ui/spinner";

export function RequireAuth({ children }: { children: ReactNode }) {
  const { me, loading } = useAuth();
  if (loading) {
    return (
      <div className="grid min-h-dvh place-items-center">
        <Spinner className="h-6 w-6" />
      </div>
    );
  }
  if (!me) return <Navigate to="/login" replace />;
  return <>{children}</>;
}

export function RequireRole({ min, children }: { min: Role; children: ReactNode }) {
  const { role } = useAuth();
  const rank = role ? roleRank[role] : 0;
  if (rank < roleRank[min]) {
    return (
      <p className="py-16 text-center text-sm text-muted-foreground">
        This page requires the <span className="font-medium">{min}</span> role.
      </p>
    );
  }
  return <>{children}</>;
}
