// Writes from the notes pages carry the session's CSRF token. Private auth
// rejects any non-GET request without it ("invalid CSRF token"), so every
// mutation goes through mutate(): it fetches the token once, attaches it,
// and refetches it once if the session was renewed underneath us.

let token: Promise<string> | null = null

function base(): string {
  const b = document.body?.dataset.base || "/"
  return b.endsWith("/") ? b : b + "/"
}

async function loadToken(): Promise<string> {
  const res = await fetch(`${base()}kanban/api/session`, { credentials: "same-origin", cache: "no-store" })
  if (!res.ok) return ""
  const session = (await res.json()) as { csrf?: string }
  return session.csrf || ""
}

export function withCSRF(init: RequestInit, csrf: string): RequestInit {
  const headers = new Headers(init.headers)
  if (csrf) headers.set("X-CSRF-Token", csrf)
  return { credentials: "same-origin", ...init, headers }
}

export async function mutate(url: string, init: RequestInit): Promise<Response> {
  token ??= loadToken()
  let res = await fetch(url, withCSRF(init, await token))
  if (res.status === 403) {
    const text = await res.clone().text()
    if (text.includes("CSRF")) {
      token = loadToken()
      res = await fetch(url, withCSRF(init, await token))
    }
  }
  return res
}
