/**
 * turnstile.ts -- a stand-in for Cloudflare's Turnstile widget API.
 *
 * @speed/ui-kit's HumanVerification block renders the widget through
 * `window.turnstile` in explicit-render mode. A jsdom case installs this
 * fake before rendering: it records each widget and the options it was
 * rendered with, and exposes the callbacks so a case can answer a token,
 * expire the widget, or assert the reset and remove calls the block makes
 * when the host consumes a token.
 */

/** One widget the block rendered. */
export interface FakeTurnstileWidget {
  readonly id: string
  readonly container: HTMLElement
  readonly siteKey: string
  /** Answer the widget's token callback. */
  token(value: string): void
  /** Fire the widget's expiry callback. */
  expire(): void
  /** Fire the widget's error callback. */
  fail(): void
}

/** The installed fake and the handles a case drives it with. */
export interface FakeTurnstile {
  readonly widgets: FakeTurnstileWidget[]
  /** Widget ids the block reset, in order. */
  readonly resets: string[]
  /** Widget ids the block removed, in order. */
  readonly removed: string[]
  /** Answer the most recently rendered widget with a token. */
  token(value: string): void
  /** Expire the most recently rendered widget. */
  expire(): void
  /** Fire the most recently rendered widget's error callback. */
  fail(): void
  /** Remove the fake (or restore the previous API). */
  restore(): void
}

/** The fake's own view of the options the block passes. */
interface FakeRenderOptions {
  readonly sitekey: string
  readonly callback: (token: string) => void
  readonly 'expired-callback'?: () => void
  readonly 'error-callback'?: () => void
}

let widgetCounter = 0

/**
 * Install the fake on `window.turnstile` and return the handles. Call
 * `restore()` in the case's cleanup so the next case starts without it.
 */
export function installFakeTurnstile(): FakeTurnstile {
  widgetCounter = 0
  const previous = (window as unknown as Record<string, unknown>).turnstile
  const widgets: FakeTurnstileWidget[] = []
  const resets: string[] = []
  const removed: string[] = []
  const api = {
    render(container: HTMLElement, options: FakeRenderOptions): string {
      widgetCounter += 1
      const id = `widget-${widgetCounter}`
      widgets.push({
        id,
        container,
        siteKey: options.sitekey,
        token: (value) => options.callback(value),
        expire: () => options['expired-callback']?.(),
        fail: () => options['error-callback']?.(),
      })
      return id
    },
    reset(id: string): void {
      resets.push(id)
    },
    remove(id: string): void {
      removed.push(id)
    },
  }
  ;(window as unknown as Record<string, unknown>).turnstile = api
  const latest = (): FakeTurnstileWidget | undefined => widgets.at(-1)
  return {
    widgets,
    resets,
    removed,
    token: (value) => latest()?.token(value),
    expire: () => latest()?.expire(),
    fail: () => latest()?.fail(),
    restore: () => {
      if (previous === undefined) {
        delete (window as unknown as Record<string, unknown>).turnstile
      } else {
        ;(window as unknown as Record<string, unknown>).turnstile = previous
      }
    },
  }
}
