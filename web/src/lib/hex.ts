// utf8ToHex encodes a string as the hex of its UTF-8 bytes. CIP-30 signData
// takes a hex payload; the issuer compares the COSE payload to []byte(nonce),
// so the nonce must be sent as hex(utf8(nonce)) — a known footgun (S0003 §2.5).
export function utf8ToHex(s: string): string {
  const bytes = new TextEncoder().encode(s);
  let out = "";
  for (const b of bytes) out += b.toString(16).padStart(2, "0");
  return out;
}
