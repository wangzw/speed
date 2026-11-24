/** The dev server's IPv4 loopback address. Never bind ::1: 127.0.0.1 and localhost must both reach it. */
export const DEMO_WEB_DEV_HOST = '127.0.0.1'

export const DEMO_WEB_DEFAULT_PORT = 8080

export const DEMO_WEB_DEFAULT_BACKEND_PROXY = 'http://127.0.0.1:8081'

export interface DevServerPlan {
  readonly host: string
  readonly port: number
  readonly strictPort: true
  readonly proxyTarget: string
}

export function devServerPlan(
  env: Readonly<Record<string, string | undefined>>,
): DevServerPlan {
  return {
    host: DEMO_WEB_DEV_HOST,
    port: devServerPort(env.DEMO_WEB_PORT),
    strictPort: true,
    proxyTarget: env.DEMO_WEB_BACKEND_PROXY ?? DEMO_WEB_DEFAULT_BACKEND_PROXY,
  }
}

/** Parses the port override; a set value must be a whole number in range and fails the load otherwise. */
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
