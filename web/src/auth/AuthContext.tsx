import { useQuery, useQueryClient } from "@tanstack/react-query";
import { createContext, useContext, type ReactNode } from "react";
import { adminChallenge, adminLogout, adminMe, adminVerify } from "@/api/admin";
import { ApiError } from "@/api/client";
import type { Me, Role } from "@/lib/types";
import { connectWallet } from "@/wallet/adapter";

interface AuthState {
  me: Me | null;
  role: Role | null;
  loading: boolean;
  login: (walletKey: string) => Promise<void>;
  logout: () => Promise<void>;
}

const AuthContext = createContext<AuthState | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const qc = useQueryClient();

  const meQuery = useQuery<Me | null>({
    queryKey: ["me"],
    queryFn: async () => {
      try {
        return await adminMe();
      } catch (e) {
        if (e instanceof ApiError && e.isUnauthorized) return null;
        throw e;
      }
    },
    retry: false,
    staleTime: 30_000,
  });

  // login: connect wallet -> challenge(reward address) -> signData -> verify
  // (the issuer recovers the owner vkey from the COSE_Key and sets the cookie).
  async function login(walletKey: string) {
    const session = await connectWallet(walletKey);
    const { nonce } = await adminChallenge(session.rewardAddress);
    const { coseKeyHex, signatureHex } = await session.signNonce(nonce);
    await adminVerify({ nonce, cose_key: coseKeyHex, signature: signatureHex });
    await qc.invalidateQueries({ queryKey: ["me"] });
  }

  async function logout() {
    try {
      await adminLogout();
    } finally {
      qc.setQueryData(["me"], null);
      qc.clear();
    }
  }

  const me = meQuery.data ?? null;
  return (
    <AuthContext.Provider
      value={{ me, role: me?.role ?? null, loading: meQuery.isLoading, login, logout }}
    >
      {children}
    </AuthContext.Provider>
  );
}

export function useAuth(): AuthState {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used within <AuthProvider>");
  return ctx;
}
