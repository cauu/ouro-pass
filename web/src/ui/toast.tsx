import {
  createContext,
  useCallback,
  useContext,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { cn } from "@/lib/cn";

type Variant = "default" | "success" | "destructive";
interface ToastItem {
  id: number;
  title: string;
  description?: string;
  variant: Variant;
}
interface ToastApi {
  toast: (t: { title: string; description?: string; variant?: Variant }) => void;
}

const ToastContext = createContext<ToastApi | null>(null);

export function ToastProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<ToastItem[]>([]);
  const seq = useRef(0);

  const toast = useCallback<ToastApi["toast"]>((t) => {
    const id = ++seq.current;
    setItems((prev) => [...prev, { id, variant: "default", ...t }]);
    setTimeout(() => setItems((prev) => prev.filter((x) => x.id !== id)), 5000);
  }, []);

  return (
    <ToastContext.Provider value={{ toast }}>
      {children}
      <div className="fixed bottom-4 right-4 z-[100] flex w-80 max-w-[calc(100vw-2rem)] flex-col gap-2">
        {items.map((t) => (
          <div
            key={t.id}
            role="status"
            className={cn(
              "rounded-md border bg-card p-3 shadow-lg",
              t.variant === "destructive" && "border-destructive",
              t.variant === "success" && "border-success",
            )}
          >
            <div className="text-sm font-medium">{t.title}</div>
            {t.description && (
              <div className="mt-0.5 break-words text-xs text-muted-foreground">{t.description}</div>
            )}
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  );
}

export function useToast(): ToastApi {
  const ctx = useContext(ToastContext);
  if (!ctx) throw new Error("useToast must be used within <ToastProvider>");
  return ctx;
}
