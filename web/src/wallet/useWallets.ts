import { useEffect, useState } from "react";
import { listWallets } from "./adapter";
import type { WalletInfo } from "./types";

// Wallets inject on window.cardano at different (sometimes late) times, so poll
// for a few seconds and react to the cardano#initialized signal (S0003 lesson).
export function useWallets(): WalletInfo[] {
  const [wallets, setWallets] = useState<WalletInfo[]>(() => listWallets());
  useEffect(() => {
    let tries = 0;
    const refresh = () =>
      setWallets((prev) => {
        const found = listWallets();
        return prev.length !== found.length ? found : prev;
      });
    const timer = setInterval(() => {
      refresh();
      if (listWallets().length > 0 || ++tries >= 24) clearInterval(timer);
    }, 250);
    window.addEventListener("cardano#initialized", refresh);
    window.addEventListener("load", refresh);
    return () => {
      clearInterval(timer);
      window.removeEventListener("cardano#initialized", refresh);
      window.removeEventListener("load", refresh);
    };
  }, []);
  return wallets;
}
