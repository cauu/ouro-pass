import type { ReactNode } from "react";
import { cn } from "@/lib/cn";

// Consistent empty state (S0007): icon + title + description + optional action,
// replacing the one-line muted text used across list pages.
export function EmptyState({
  icon,
  title,
  description,
  action,
  className,
}: {
  icon?: ReactNode;
  title: string;
  description?: ReactNode;
  action?: ReactNode;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "flex flex-col items-center justify-center rounded-lg border border-dashed bg-card/40 px-6 py-14 text-center",
        className,
      )}
    >
      {icon ? (
        <div className="mb-3 grid h-11 w-11 place-items-center rounded-xl border bg-surface text-muted-foreground">
          {icon}
        </div>
      ) : null}
      <h3 className="text-sm font-semibold">{title}</h3>
      {description ? (
        <p className="mt-1 max-w-sm text-sm text-muted-foreground">{description}</p>
      ) : null}
      {action ? <div className="mt-4">{action}</div> : null}
    </div>
  );
}
