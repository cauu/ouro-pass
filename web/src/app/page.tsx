import type { ReactNode } from "react";
import { Spinner } from "@/ui/spinner";

export function PageHeader({
  title,
  description,
  action,
}: {
  title: string;
  description?: string;
  action?: ReactNode;
}) {
  return (
    <div className="mb-6 flex items-start justify-between gap-4">
      <div>
        <h1 className="text-xl font-semibold">{title}</h1>
        {description ? <p className="mt-1 text-sm text-muted-foreground">{description}</p> : null}
      </div>
      {action}
    </div>
  );
}

export function QueryState({
  isLoading,
  error,
  empty,
  emptyText,
  children,
}: {
  isLoading: boolean;
  error: unknown;
  empty?: boolean;
  emptyText?: string;
  children: ReactNode;
}) {
  if (isLoading) {
    return (
      <div className="grid place-items-center py-16">
        <Spinner className="h-5 w-5" />
      </div>
    );
  }
  if (error) {
    return (
      <p className="py-8 text-sm text-destructive">
        {error instanceof Error ? error.message : "Failed to load."}
      </p>
    );
  }
  if (empty) {
    return <p className="py-8 text-sm text-muted-foreground">{emptyText ?? "Nothing here yet."}</p>;
  }
  return <>{children}</>;
}
