export interface FakeTurnstileWidget {
  readonly id: string
  readonly container: HTMLElement
  readonly siteKey: string
  readonly action: string | undefined
  token(value: string): void
  expire(): void
  fail(): void
}

export interface FakeTurnstile {
  readonly widgets: FakeTurnstileWidget[]
  readonly resets: string[]
  readonly removed: string[]
  token(value: string): void
  expire(): void
  fail(): void
  restore(): void
}

interface FakeRenderOptions {
  readonly sitekey: string
  readonly action?: string
  readonly callback: (token: string) => void
  readonly 'expired-callback'?: () => void
  readonly 'error-callback'?: () => void
}

let widgetCounter = 0

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
        action: options.action,
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
