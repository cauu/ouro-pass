import { cn } from "@/lib/cn";

// Loading placeholder block (S0007). Used by QueryState and stat cards instead
// of the bare "…" / spinner that read as broken.
export function Skeleton({ className }: { className?: string }) {
  return <div className={cn("animate-pulse rounded-md bg-muted", className)} />;
}
