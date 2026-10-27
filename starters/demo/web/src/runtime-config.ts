/**
 * The deployment's runtime configuration, read from beside the entry document.
 *
 * One built dist is shared across deployment topologies (a single process
 * serving API and page, or a page on a static host with the API elsewhere), so
 * deployment facts are not baked into the bundle: they are read at startup
 * from runtime-config.json, fetched at the deployment root so the read never
 * needs an already-resolved origin to resolve one and a deep link cannot shift
 * it under the page's own path. The file names the API origin (absent means
 * the page's own origin, the same-origin deployment shape), the shell layout
 * and the default theme preset.
 *
 * Every field is read on its own: a malformed value for one leaves the others
 * intact, so a typo in `apiOrigin` cannot silently drop the chosen layout or
 * preset, and an unknown value is ignored rather than propagated.
 *
 * This module is the app's one hand-written request, and the reason
 * eslint.config.mjs exempts it from the no-direct-http rule: it reads a static
 * deployment asset, not an API endpoint, and the contract that puts it there
 * is the runtime-config decision (docs/adr/adr-web-runtime-config.md).
 * It runs before the client is built, so it cannot go through the client.
 */

import type { AppShellShellLayout } from '@speed/layout-kit'
import { themePresets } from '@speed/theme-presets'

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

/** The deployment facts the app reads before it renders. */
export interface RuntimeConfig {
  /** The API origin, e.g. "https://api.example.com". Absent means same-origin. */
  readonly apiOrigin?: string
  /** The shell form the deployment renders. Absent means the top bar. */
  readonly shellLayout?: AppShellShellLayout
  /** The preset a device with no stored choice starts on. Absent means 'default'. */
  readonly themePreset?: string
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

/**
 * Extracts the recognized fields from a parsed body, ignoring anything else.
 * Each field is taken on its own: a value that does not match its contract is
 * dropped while the rest of the body still applies.
 */
function readRuntimeConfig(body: unknown): RuntimeConfig {
  if (typeof body !== 'object' || body === null) {
    return {}
  }
  const record = body as Record<string, unknown>
  const config: { -readonly [K in keyof RuntimeConfig]: RuntimeConfig[K] } = {}

  const apiOrigin = record.apiOrigin
  if (typeof apiOrigin === 'string' && apiOrigin.length > 0) {
    config.apiOrigin = apiOrigin
  }

  const shellLayout = record.shellLayout
  if (isShellLayout(shellLayout)) {
    config.shellLayout = shellLayout
  }

  const themePreset = record.themePreset
  if (isThemePreset(themePreset)) {
    config.themePreset = themePreset
  }

  return config
}

/** Whether a value names one of the shell layouts the frame implements. */
function isShellLayout(value: unknown): value is AppShellShellLayout {
  return value === 'topbar' || value === 'sidebar'
}

/** Whether a value names a preset the app ships; an unknown id is ignored so
 * the provider falls back to its documented default instead of receiving one. */
function isThemePreset(value: unknown): value is string {
  return (
    typeof value === 'string' &&
    themePresets.some((preset) => preset.id === value)
  )
}
