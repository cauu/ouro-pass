import { Button } from "@/ui/button";
import { Spinner } from "@/ui/spinner";
import { useWallets } from "@/wallet/useWallets";

/** WalletPicker lists the injected CIP-30 wallets and calls onPick with the chosen key. */
export function WalletPicker({
  onPick,
  busy,
}: {
  onPick: (key: string) => void;
  busy?: string | null;
}) {
  const wallets = useWallets();

  if (wallets.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        Detecting wallet… If none appears, install Nami, Eternl, Lace, Vespr, … and reload.
      </p>
    );
  }

  return (
    <div className="flex flex-col gap-2">
      {wallets.map((w) => (
        <Button
          key={w.key}
          variant="outline"
          className="justify-start"
          disabled={!!busy}
          onClick={() => onPick(w.key)}
        >
          {w.icon ? <img src={w.icon} alt="" className="h-5 w-5 rounded" /> : null}
          <span>{w.name}</span>
          {busy === w.key ? <Spinner className="ml-auto" /> : null}
        </Button>
      ))}
    </div>
  );
}
