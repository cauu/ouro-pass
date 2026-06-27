import { Copy } from "lucide-react";
import { cn } from "@/lib/cn";
import { useToast } from "./toast";

// Inline monospace value with a hover copy affordance (S0007). Replaces the bare
// truncated <span title=…> used for stake hashes / client ids / kids, giving
// operators a one-click copy of the full value.
export function CopyButton({
  value,
  display,
  className,
  toastLabel = "Copied to clipboard",
}: {
  value: string;
  display?: string;
  className?: string;
  toastLabel?: string;
}) {
  const { toast } = useToast();
  async function onCopy() {
    try {
      await navigator.clipboard?.writeText(value);
      toast({ title: toastLabel, description: value });
    } catch {
      toast({ title: "Copy failed", variant: "destructive" });
    }
  }
  return (
    <button
      type="button"
      onClick={onCopy}
      title={value}
      className={cn(
        "group inline-flex max-w-full items-center gap-1.5 rounded px-1 py-0.5 font-mono text-xs transition-colors hover:bg-muted",
        className,
      )}
    >
      <span className="truncate">{display ?? value}</span>
      <Copy className="h-3 w-3 shrink-0 text-muted-foreground opacity-0 transition-opacity group-hover:opacity-100" />
    </button>
  );
}
