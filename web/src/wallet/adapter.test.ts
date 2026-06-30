import { afterEach, describe, expect, it, vi } from "vitest";
import { connectWallet, listWallets, networkName } from "./adapter";
import { utf8ToHex } from "@/lib/hex";
import type { Cip30Wallet } from "./types";

function fakeWallet(over: Partial<{ networkId: number; rewards: string[]; name: string }> = {}) {
  const signData = vi.fn(async (_addr: string, _payloadHex: string) => ({
    signature: "84a1deadbeef", // opaque COSE_Sign1 hex — forwarded as-is
    key: "a4010103272006215820aa", // opaque COSE_Key hex — forwarded as-is
  }));
  const wallet: Cip30Wallet = {
    name: over.name ?? "Mocky",
    icon: "data:image/png;base64,AA",
    apiVersion: "0.1.0",
    enable: vi.fn(async () => ({
      getNetworkId: vi.fn(async () => over.networkId ?? 0),
      getRewardAddresses: vi.fn(async () => over.rewards ?? ["e0deadbeef"]),
      signData,
    })),
  };
  return { wallet, signData };
}

afterEach(() => {
  delete (window as { cardano?: unknown }).cardano;
  vi.restoreAllMocks();
});

describe("listWallets", () => {
  it("discovers CIP-30 wallets and ignores non-wallet props", () => {
    const { wallet } = fakeWallet({ name: "Vespr" });
    window.cardano = {
      vespr: wallet,
      // a non-wallet helper object that must be skipped
      enable: { foo: 1 } as unknown as Cip30Wallet,
    };
    const found = listWallets();
    expect(found.map((w) => w.key)).toEqual(["vespr"]);
    expect(found[0].name).toBe("Vespr");
  });

  it("returns empty when no wallet is injected", () => {
    expect(listWallets()).toEqual([]);
  });
});

describe("connectWallet", () => {
  it("forwards the COSE_Key + signature without decoding CBOR, signing hex(utf8(nonce))", async () => {
    const { wallet, signData } = fakeWallet({ rewards: ["e0abc123"] });
    window.cardano = { mocky: wallet };

    const session = await connectWallet("mocky");
    expect(session.rewardAddress).toBe("e0abc123");

    const nonce = "challenge-NONCE_42";
    const out = await session.signNonce(nonce);
    // signData called with the reward address and hex(utf8(nonce)).
    expect(signData).toHaveBeenCalledWith("e0abc123", utf8ToHex(nonce));
    // The adapter forwards the wallet's key/signature verbatim (no CBOR parsing).
    expect(out).toEqual({ coseKeyHex: "a4010103272006215820aa", signatureHex: "84a1deadbeef" });
  });

  it("is network-agnostic: connects regardless of the wallet's network (S0014 p1-4)", async () => {
    const { wallet } = fakeWallet({ networkId: 0 }); // testnet wallet, no guard
    window.cardano = { mocky: wallet };
    await expect(connectWallet("mocky")).resolves.toMatchObject({ key: "mocky" });
  });

  it("rejects when the wallet has no reward address", async () => {
    const { wallet } = fakeWallet({ rewards: [] });
    window.cardano = { mocky: wallet };
    await expect(connectWallet("mocky")).rejects.toThrow(/no stake/);
  });

  it("throws for an unknown wallet key", async () => {
    window.cardano = {};
    await expect(connectWallet("ghost")).rejects.toThrow(/not found/);
  });
});

describe("networkName", () => {
  it("maps CIP-30 network ids", () => {
    expect(networkName(1)).toBe("mainnet");
    expect(networkName(0)).toBe("testnet");
  });
});
