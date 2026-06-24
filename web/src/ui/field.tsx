import type { ReactNode } from "react";
import { Label } from "./label";

export function Field({
  label,
  error,
  hint,
  children,
}: {
  label: string;
  error?: string;
  hint?: string;
  children: ReactNode;
}) {
  return (
    <div className="grid gap-1.5">
      <Label>{label}</Label>
      {children}
      {error ? <span className="text-xs text-destructive">{error}</span> : null}
      {hint && !error ? <span className="text-xs text-muted-foreground">{hint}</span> : null}
    </div>
  );
}
