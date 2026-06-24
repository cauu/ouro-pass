import { useCallback } from "react";
import { stepUpChallenge } from "@/api/admin";
import { connectWallet } from "@/wallet/adapter";
import { config } from "@/lib/config";
import type { StepUpBody } from "@/lib/types";

// useStepUp returns a function that runs the §9.8 step-up flow for a sensitive
// operation: connect the wallet, fetch a step-up nonce, sign it, and return the
// {cose_key, step_up_nonce, step_up_signature} body the backend re-verifies.
export function useStepUp() {
  return useCallback(async (walletKey: string): Promise<StepUpBody> => {
    const session = await connectWallet(walletKey, config.issuerNetwork);
    const { nonce } = await stepUpChallenge(session.rewardAddress);
    const { coseKeyHex, signatureHex } = await session.signNonce(nonce);
    return { cose_key: coseKeyHex, step_up_nonce: nonce, step_up_signature: signatureHex };
  }, []);
}
