// 后台 API 的类型和调用。类型与 internal/api 的 JSON 字段一一对应。

export type VersionInfo = {
  number: number
  kind: 'site' | 'assets'
  note: string
  files: number
  bytes: number
  created_at: string
  url: string
  origin?: 'upload' | 'rollback' | 'build'
  current?: boolean
}

export type ProxyInfo = {
  port: number
  host_mode: 'localhost' | 'preserve'
  status: 'active' | 'paused'
  paused_reason?: 'process_changed' | 'unreachable'
  last_seen_at: string | null
}

export type Prototype = {
  name: string
  serve_kind: 'static' | 'proxy'
  title: string
  preview_url: string
  created_at: string
  updated_at: string
  current: VersionInfo | null
  versions: number
  proxy: ProxyInfo | null
}

export type Share = {
  id: number
  prototype: string
  url: string
  note: string
  has_password: boolean
  status: 'active' | 'expired' | 'revoked'
  created_at: string
  expires_at: string | null
  revoked_at: string | null
}

export type Token = {
  id: number
  name: string
  hint: string
  scopes: string[]
  status: 'active' | 'expired' | 'revoked'
  created_at: string
  expires_at: string | null
  last_used_at: string | null
  revoked_at: string | null
  token?: string
}

export type PushResult = {
  prototype: { name: string; kind: string; created: boolean }
  version: { number: number; unchanged: boolean; note: string }
  urls: { preview: string; version: string }
  stats: { files: number; bytes: number; skipped: string[] }
  warnings: string[]
}

export type WhoAmI = {
  kind: 'local' | 'admin' | 'token'
  scopes: string[]
  server: { app_url: string; content_url: string }
}

export class ApiError extends Error {
  status: number
  code: string
  hint?: string
  constructor(status: number, code: string, message: string, hint?: string) {
    super(message)
    this.status = status
    this.code = code
    this.hint = hint
  }
}

/** 会话过期或被注销：回到登录页，登录后再回来。 */
export function redirectToLogin() {
  const next = window.location.pathname + window.location.search
  window.location.assign('/auth/login?' + new URLSearchParams({ next }).toString())
}

async function request<T>(method: string, path: string, body?: unknown, raw?: BodyInit): Promise<T> {
  const headers: Record<string, string> = {}
  let payload: BodyInit | undefined = raw
  if (body !== undefined) {
    headers['Content-Type'] = 'application/json'
    payload = JSON.stringify(body)
  }
  const resp = await fetch(path, { method, headers, body: payload, credentials: 'same-origin' })
  if (resp.status === 401) {
    redirectToLogin()
    throw new ApiError(401, 'unauthorized', '登录已过期')
  }
  if (resp.status === 204) return undefined as T
  const data = await resp.json().catch(() => null)
  if (!resp.ok) {
    const e = data?.error
    throw new ApiError(resp.status, e?.code ?? 'http_error', e?.message ?? `请求失败（${resp.status}）`, e?.hint)
  }
  return data as T
}

const enc = encodeURIComponent

export const api = {
  whoami: () => request<WhoAmI>('GET', '/api/v1/whoami'),
  prototypes: () => request<{ prototypes: Prototype[] }>('GET', '/api/v1/prototypes').then((r) => r.prototypes),
  prototype: (name: string) => request<Prototype>('GET', `/api/v1/prototypes/${enc(name)}`),
  deletePrototype: (name: string) => request<void>('DELETE', `/api/v1/prototypes/${enc(name)}`),
  versions: (name: string) =>
    request<{ versions: VersionInfo[] }>('GET', `/api/v1/prototypes/${enc(name)}/versions`).then((r) => r.versions),
  rollback: (name: string, version: number) =>
    request<PushResult>('POST', `/api/v1/prototypes/${enc(name)}/rollback`, { version }),
  shares: (prototype?: string) =>
    request<{ shares: Share[] }>('GET', '/api/v1/shares' + (prototype ? `?prototype=${enc(prototype)}` : '')).then(
      (r) => r.shares,
    ),
  createShare: (prototype: string, req: { note?: string; password?: string; expires_in_seconds?: number }) =>
    request<Share>('POST', `/api/v1/prototypes/${enc(prototype)}/shares`, req),
  revokeShare: (id: number) => request<void>('DELETE', `/api/v1/shares/${id}`),
  tokens: () => request<{ tokens: Token[] }>('GET', '/api/v1/tokens').then((r) => r.tokens),
  createToken: (req: { name: string; scopes: string[]; expires_in_seconds?: number }) =>
    request<Token>('POST', '/api/v1/tokens', req),
  revokeToken: (id: number) => request<void>('DELETE', `/api/v1/tokens/${id}`),
  /** 整包推送：body 是 tar / tar.gz / zip。 */
  push: (name: string, body: Blob, opts: { note?: string; spa?: boolean } = {}) => {
    const q = new URLSearchParams({ name })
    if (opts.note) q.set('note', opts.note)
    if (opts.spa) q.set('spa', 'true')
    return request<PushResult>('POST', `/api/v1/push?${q}`, undefined, body)
  },
}
