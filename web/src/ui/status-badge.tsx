import { cn } from "@/lib/cn";
import { Badge } from "./badge";

type Variant = "success" | "warning" | "destructive" | "muted" | "info" | "default";

// Single source of truth for status → badge variant across the admin (S0007).
// Keeps every table's status column visually consistent and avoids the per-page
// `statusVariant` helpers that drifted apart before.
const MAP: Record<string, Variant> = {
  // healthy / active
  active: "success",
  done: "success",
  valid: "success",
  eligible_member: "success",
  synced: "success",
  // in-flight / transitional
  grace: "warning",
  sending: "warning",
  scheduled: "warning",
  pending: "warning",
  rotating: "warning",
  syncing: "warning",
  // terminal / negative
  failed: "destructive",
  cancelled: "destructive",
  expired: "destructive",
  revoked: "destructive",
  // inert
  disabled: "muted",
  retired: "muted",
  no_session: "muted",
};

export function StatusBadge({
  status,
  label,
  dot = true,
  className,
}: {
  status: string;
  label?: string;
  dot?: boolean;
  className?: string;
}) {
  const variant = MAP[status.toLowerCase()] ?? "muted";
  return (
    <Badge variant={variant} className={cn("gap-1.5", className)}>
      {dot ? <span className="h-1.5 w-1.5 rounded-full bg-current opacity-80" /> : null}
      {label ?? status}
    </Badge>
  );
}
