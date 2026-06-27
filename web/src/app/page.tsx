import { AlertCircle } from "lucide-react";
import type { ReactNode } from "react";
import { EmptyState } from "@/ui/empty-state";
import { Skeleton } from "@/ui/skeleton";

export function PageHeader({
  title,
  description,
  action,
}: {
  title: string;
  description?: ReactNode;
  action?: ReactNode;
}) {
  return (
    <div className="mb-6 flex items-start justify-between gap-6">
      <div>
        <h1 className="text-[21px] font-semibold tracking-tight">{title}</h1>
        {description ? (
          <p className="mt-1 max-w-2xl text-sm text-muted-foreground">{description}</p>
        ) : null}
      </div>
      {action ? <div className="flex shrink-0 items-center gap-2">{action}</div> : null}
    </div>
  );
}

function TableSkeleton() {
  return (
    <div className="space-y-3 rounded-lg border bg-card p-4 shadow-sm">
      {Array.from({ length: 5 }).map((_, i) => (
        <div key={i} className="flex items-center gap-4">
          <Skeleton className="h-4 w-1/4" />
          <Skeleton className="h-4 w-1/6" />
          <Skeleton className="h-4 w-1/5" />
          <Skeleton className="ml-auto h-4 w-16" />
        </div>
      ))}
    </div>
  );
}

export function QueryState({
  isLoading,
  error,
  empty,
  emptyText,
  emptyTitle,
  children,
}: {
  isLoading: boolean;
  error: unknown;
  empty?: boolean;
  emptyText?: string;
  emptyTitle?: string;
  children: ReactNode;
}) {
  if (isLoading) {
    return <TableSkeleton />;
  }
  if (error) {
    return (
      <div className="flex items-start gap-3 rounded-lg border border-destructive/40 bg-destructive/5 p-4 text-sm">
        <AlertCircle className="mt-0.5 h-4 w-4 shrink-0 text-destructive" />
        <div>
          <div className="font-medium text-destructive">Failed to load</div>
          <div className="mt-0.5 text-muted-foreground">
            {error instanceof Error ? error.message : "Something went wrong. Try again."}
          </div>
        </div>
      </div>
    );
  }
  if (empty) {
    return <EmptyState title={emptyTitle ?? "Nothing here yet"} description={emptyText} />;
  }
  return <>{children}</>;
}
