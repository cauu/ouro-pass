import { useState, type ReactNode } from "react";
import { ApiError } from "@/api/client";
import { Button } from "./button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "./dialog";
import { Field } from "./field";
import { Input } from "./input";
import { useToast } from "./toast";

function errMsg(e: unknown): string {
  return e instanceof ApiError ? e.message : e instanceof Error ? e.message : String(e);
}

// ConfirmDialog — the styled replacement for the browser's native confirm
// dialog on destructive actions (S0007). Trigger-driven (mirrors StepUpDialog),
// awaits an async onConfirm, shows a pending state, and surfaces failures as a
// toast instead of a blocking alert.
export function ConfirmDialog({
  trigger,
  title,
  description,
  confirmLabel = "Confirm",
  destructive = false,
  onConfirm,
}: {
  trigger: ReactNode;
  title: string;
  description?: ReactNode;
  confirmLabel?: string;
  destructive?: boolean;
  onConfirm: () => Promise<unknown> | void;
}) {
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const { toast } = useToast();

  async function run() {
    setBusy(true);
    try {
      await onConfirm();
      setOpen(false);
    } catch (e) {
      toast({ title: "Action failed", description: errMsg(e), variant: "destructive" });
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !busy && setOpen(o)}>
      <DialogTrigger asChild>{trigger}</DialogTrigger>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          {description ? <DialogDescription>{description}</DialogDescription> : null}
        </DialogHeader>
        <DialogFooter>
          <Button variant="ghost" disabled={busy} onClick={() => setOpen(false)}>
            Cancel
          </Button>
          <Button variant={destructive ? "destructive" : "default"} disabled={busy} onClick={run}>
            {busy ? "Working…" : confirmLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// PromptDialog — the styled replacement for the browser's native prompt dialog
// on single-value inputs (e.g. a new bot token). Same lifecycle as
// ConfirmDialog, but passes the field value to onConfirm and requires a
// non-empty value to enable the action.
export function PromptDialog({
  trigger,
  title,
  description,
  label,
  placeholder,
  inputType = "text",
  confirmLabel = "Save",
  onConfirm,
}: {
  trigger: ReactNode;
  title: string;
  description?: ReactNode;
  label: string;
  placeholder?: string;
  inputType?: "text" | "password";
  confirmLabel?: string;
  onConfirm: (value: string) => Promise<unknown> | void;
}) {
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [value, setValue] = useState("");
  const { toast } = useToast();

  function close() {
    if (busy) return;
    setOpen(false);
    setValue("");
  }

  async function run() {
    if (!value.trim()) return;
    setBusy(true);
    try {
      await onConfirm(value.trim());
      setOpen(false);
      setValue("");
    } catch (e) {
      toast({ title: "Action failed", description: errMsg(e), variant: "destructive" });
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={(o) => (o ? setOpen(true) : close())}>
      <DialogTrigger asChild>{trigger}</DialogTrigger>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          {description ? <DialogDescription>{description}</DialogDescription> : null}
        </DialogHeader>
        <Field label={label}>
          <Input
            type={inputType}
            autoComplete="off"
            placeholder={placeholder}
            value={value}
            onChange={(e) => setValue(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && run()}
          />
        </Field>
        <DialogFooter>
          <Button variant="ghost" disabled={busy} onClick={close}>
            Cancel
          </Button>
          <Button disabled={busy || !value.trim()} onClick={run}>
            {busy ? "Working…" : confirmLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
