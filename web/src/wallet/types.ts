// Minimal CIP-30 surface the Admin SPA uses: connect, read the reward (stake)
// address, and signData. We deliberately depend on nothing else (no tx builder,
// no CBOR) — the browser only forwards the COSE_Key + signature (S0003 C3).
export interface Cip30Api {
  getNetworkId(): Promise<number>;
  getRewardAddresses(): Promise<string[]>;
  // signData(addr, payloadHex) -> DataSignature { signature(COSE_Sign1), key(COSE_Key) }
  signData(addressHex: string, payloadHex: string): Promise<{ signature: string; key: string }>;
}

export interface Cip30Wallet {
  name?: string;
  icon?: string;
  apiVersion?: string;
  enable(): Promise<Cip30Api>;
  isEnabled?(): Promise<boolean>;
}

export interface WalletInfo {
  key: string;
  name: string;
  icon: string;
}

declare global {
  interface Window {
    cardano?: Record<string, Cip30Wallet>;
  }
}
