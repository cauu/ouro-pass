import { utf8ToHex } from "@/lib/hex";
import type { Cip30Api, Cip30Wallet, WalletInfo } from "./types";

// A CIP-30 wallet exposes an object on window.cardano with enable() plus some of
// apiVersion/name/icon/isEnabled. Be lenient — shapes vary across wallets.
function isWallet(w: Cip30Wallet | undefined): w is Cip30Wallet {
  return (
    !!w &&
    typeof w.enable === "function" &&
    (!!w.apiVersion || !!w.name || !!w.icon || typeof w.isEnabled === "function")
  );
}

/** listWallets enumerates the CIP-30 wallets currently injected on window.cardano. */
export function listWallets(): WalletInfo[] {
  const c = window.cardano ?? {};
  const out: WalletInfo[] = [];
  for (const key of Object.keys(c)) {
    const w = c[key];
    if (isWallet(w)) out.push({ key, name: w.name ?? key, icon: w.icon ?? "" });
  }
  return out.sort((a, b) => a.name.localeCompare(b.name));
}

/** networkName maps a CIP-30 network id to the issuer's network vocabulary. */
export function networkName(id: number): "mainnet" | "testnet" {
  return id === 1 ? "mainnet" : "testnet";
}

/**
 * WalletSession is a connected wallet bound to a single reward (stake) address,
 * exposing exactly what the admin auth flow needs: sign a nonce and forward the
 * COSE_Key + signature (no browser-side CBOR — the issuer recovers the vkey).
 */
export interface WalletSession {
  readonly key: string;
  readonly rewardAddress: string;
  signNonce(nonce: string): Promise<{ coseKeyHex: string; signatureHex: string }>;
}

/**
 * connectWallet enables the named wallet, enforces the issuer network (when given),
 * and resolves the reward (stake) address used for both challenge and signData.
 */
export async function connectWallet(key: string, expectedNetwork?: string): Promise<WalletSession> {
  const w = window.cardano?.[key];
  if (!isWallet(w)) throw new Error(`wallet "${key}" not found`);
  const api: Cip30Api = await w.enable();

  if (expectedNetwork) {
    const want = expectedNetwork === "mainnet" ? 1 : 0;
    const got = await api.getNetworkId();
    if (got !== want) {
      throw new Error(`wallet is on the wrong network (issuer needs ${expectedNetwork})`);
    }
  }

  const addrs = await api.getRewardAddresses();
  if (!addrs || addrs.length === 0) {
    throw new Error("wallet has no stake (reward) address; register a stake key first");
  }
  const rewardAddress = addrs[0];

  return {
    key,
    rewardAddress,
    async signNonce(nonce: string) {
      const sig = await api.signData(rewardAddress, utf8ToHex(nonce));
      return { coseKeyHex: sig.key, signatureHex: sig.signature };
    },
  };
}
