// Same-origin admin API client. The session is an httpOnly cookie, so every
// request sends credentials. Errors follow the issuer envelope {error,
// error_description}; ApiError surfaces the code + HTTP status for the UI.

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
  get isUnauthorized(): boolean {
    return this.status === 401;
  }
  get isForbidden(): boolean {
    return this.status === 403;
  }
  get isRateLimited(): boolean {
    return this.status === 429;
  }
}

type ErrEnvelope = { error?: string; error_description?: string };

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    credentials: "include",
    // Admin SPA always wants live data. Without this, GET /.well-known/ouropass/
    // jwks.json is served from the browser HTTP cache (the server sends it
    // `Cache-Control: public, max-age=60` for verifiers), so after a key
    // generate/rotate the react-query refetch returns a stale empty list for up
    // to 60s. no-store bypasses the cache; the verifier-facing cache stays intact.
    cache: "no-store",
    headers: body !== undefined ? { "Content-Type": "application/json" } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });

  const text = await res.text();
  let data: unknown = null;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      /* non-JSON body (e.g. a proxy error page) */
    }
  }

  if (!res.ok) {
    const env = (data ?? {}) as ErrEnvelope;
    const code = env.error ?? `http_${res.status}`;
    const desc =
      env.error_description ??
      (res.status === 429
        ? "rate limited — slow down"
        : (res.statusText || "request failed"));
    throw new ApiError(res.status, code, desc);
  }

  return data as T;
}

export const api = {
  get: <T>(path: string) => request<T>("GET", path),
  post: <T = void>(path: string, body?: unknown) => request<T>("POST", path, body),
};
