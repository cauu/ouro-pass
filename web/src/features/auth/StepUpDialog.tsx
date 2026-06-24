import { useState, type ReactNode } from "react";
import { ApiError } from "@/api/client";
import { useStepUp } from "@/auth/useStepUp";
import type { StepUpBody } from "@/lib/types";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/ui/dialog";
import { useToast } from "@/ui/toast";
import { WalletPicker } from "./WalletPicker";

/**
 * StepUpDialog gates a sensitive operation behind a fresh owner signature (§9.8):
 * pick a wallet -> step-up challenge -> signData -> onConfirm(stepUpBody).
 */
export function StepUpDialog({
  trigger,
  title,
  description,
  onConfirm,
  onDone,
}: {
  trigger: ReactNode;
  title: string;
  description: string;
  onConfirm: (stepUp: StepUpBody) => Promise<void>;
  onDone?: () => void;
}) {
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState<string | null>(null);
  const runStepUp = useStepUp();
  const { toast } = useToast();

  async function pick(key: string) {
    setBusy(key);
    try {
      const stepUp = await runStepUp(key);
      await onConfirm(stepUp);
      toast({ title: "Done", variant: "success" });
      setOpen(false);
      onDone?.();
    } catch (e) {
      const msg =
        e instanceof ApiError ? e.message : e instanceof Error ? e.message : "operation failed";
      toast({ title: "Failed", description: msg, variant: "destructive" });
    } finally {
      setBusy(null);
    }
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>{trigger}</DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        <p className="text-sm">Approve a fresh signature with your owner wallet to continue.</p>
        <WalletPicker onPick={pick} busy={busy} />
      </DialogContent>
    </Dialog>
  );
}
