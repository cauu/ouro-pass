export function fmtTime(s: string | null | undefined): string {
  if (!s) return "—";
  const d = new Date(s);
  return Number.isNaN(d.getTime()) ? s : d.toLocaleString();
}

export function short(s: string, n = 14): string {
  return s.length > n ? `${s.slice(0, n)}…` : s;
}
