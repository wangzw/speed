/**
 * The dev server's port and backend target, read from the environment.
 *
 * The local loop runs several checkouts side by side, so the port is a run
 * value rather than a constant: `DEMO_WEB_PORT` moves the dev server off 8080
 * and `DEMO_WEB_BACKEND_PROXY` moves the proxy target off the compiled
 * backend listener at 127.0.0.1:8081. An override that is not a port is a
 * configuration error, not something to fall back from silently, so parsing
 * lives here where a case can hold it.
 */

/** The dev server's port when DEMO_WEB_PORT is unset. */
export const DEMO_WEB_DEFAULT_PORT = 8080

/** The proxy target when DEMO_WEB_BACKEND_PROXY is unset. */
export const DEMO_WEB_DEFAULT_BACKEND_PROXY = 'http://127.0.0.1:8081'

/** The dev server's resolved settings. */
export interface DevServerPlan {
  readonly port: number
  /** Fail instead of silently moving to another port when this one is taken. */
  readonly strictPort: true
  /** The origin every claimed backend prefix proxies to. */
  readonly proxyTarget: string
}

/**
 * Resolve the dev server's settings from an environment. An unset variable
 * takes its default; a set one must be a whole number in the port range, and
 * anything else throws so `vite` reports it while loading the config.
 */
export function devServerPlan(
  env: Readonly<Record<string, string | undefined>>,
): DevServerPlan {
  return {
    port: devServerPort(env.DEMO_WEB_PORT),
    strictPort: true,
    proxyTarget: env.DEMO_WEB_BACKEND_PROXY ?? DEMO_WEB_DEFAULT_BACKEND_PROXY,
  }
}

/** Parse DEMO_WEB_PORT, or answer the default when it is unset. */
export function devServerPort(raw: string | undefined): number {
  if (raw === undefined) return DEMO_WEB_DEFAULT_PORT
  const port = /^[0-9]+$/.test(raw) ? Number(raw) : Number.NaN
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    throw new Error(
      `DEMO_WEB_PORT must be a whole number between 1 and 65535, got ${JSON.stringify(raw)}`,
    )
  }
  return port
}
