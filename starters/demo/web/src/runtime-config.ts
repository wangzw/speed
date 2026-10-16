/**
 * The deployment's runtime API origin, read from beside the entry document.
 *
 * One built dist is shared across deployment topologies (a single process
 * serving API and page, or a page on a static host with the API elsewhere), so
 * the API origin is not baked into the bundle: it is read at startup from
 * runtime-config.json, fetched at the deployment root so the read never needs
 * an already-resolved origin to resolve one and a deep link cannot shift it
 * under the page's own path. When the file is absent, unreadable, or carries
 * no origin, the caller falls back to the page's own origin (the same-origin
 * deployment shape).
 *
 * This module is the app's one hand-written request, and the reason
 * eslint.config.mjs exempts it from the no-direct-http rule: it reads a static
 * deployment asset, not an API endpoint, and the contract that puts it there
 * is the runtime-config decision (docs/adr/adr-web-runtime-config-2026-10-15.md).
 * It runs before the client is built, so it cannot go through the client.
 */

/** The file the deployment places beside the entry document. */
export const RUNTIME_CONFIG_FILE = 'runtime-config.json'

/**
 * The absolute URL the config is read from: the file at the deployment root,
 * under the entry document's own origin. It is resolved against the document
 * base with a root-absolute path, NOT left for fetch to resolve against the
 * current URL: a deep link such as /console/tenants would otherwise shift the
 * read to /console/runtime-config.json. The read still never needs an
 * already-resolved API origin (the whole point of reading a file rather than
 * asking the API), so the same-origin and cross-origin shapes both hold.
 */
export function runtimeConfigUrl(): string {
  return new URL(`/${RUNTIME_CONFIG_FILE}`, document.baseURI).href
}

/** The origin the app should reach the API at, when the deployment names one. */
export interface RuntimeConfig {
  /** The API origin, e.g. "https://api.example.com". Absent means same-origin. */
  readonly apiOrigin?: string
}

/**
 * Reads the deployment's runtime configuration. Never rejects: a missing file,
 * a network failure, a non-2xx answer or an unreadable body each yield an empty
 * configuration, which the caller treats as same-origin.
 */
export async function loadRuntimeConfig(): Promise<RuntimeConfig> {
  let response: Response
  try {
    response = await fetch(runtimeConfigUrl(), { cache: 'no-store' })
  } catch {
    return {}
  }
  if (!response.ok) {
    return {}
  }
  let body: unknown
  try {
    body = await response.json()
  } catch {
    return {}
  }
  return readRuntimeConfig(body)
}

/** Extracts the origin from a parsed body, ignoring anything else. */
function readRuntimeConfig(body: unknown): RuntimeConfig {
  if (typeof body !== 'object' || body === null) {
    return {}
  }
  const apiOrigin = (body as Record<string, unknown>).apiOrigin
  if (typeof apiOrigin !== 'string' || apiOrigin.length === 0) {
    return {}
  }
  return { apiOrigin }
}
