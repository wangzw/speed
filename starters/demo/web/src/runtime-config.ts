import type { AppShellShellLayout } from '@speed/layout-kit'
import { themePresets } from '@speed/theme-presets'

export const RUNTIME_CONFIG_FILE = 'runtime-config.json'

export function runtimeConfigUrl(): string {
  return new URL(`/${RUNTIME_CONFIG_FILE}`, document.baseURI).href
}

export interface RuntimeConfig {
  readonly apiOrigin?: string
  readonly shellLayout?: AppShellShellLayout
  readonly themePreset?: string
}

/** Reads the deployment facts; never rejects -- a missing file, network failure, non-2xx or unreadable body yields an empty config. */
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

function isShellLayout(value: unknown): value is AppShellShellLayout {
  return value === 'topbar' || value === 'sidebar'
}

function isThemePreset(value: unknown): value is string {
  return (
    typeof value === 'string' &&
    themePresets.some((preset) => preset.id === value)
  )
}
