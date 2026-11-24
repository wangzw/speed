import { vi } from 'vitest'

export interface MatchMediaHandle {
  changeMatches(matches: boolean): void
}

type ChangeListener = (event: { matches: boolean; media: string }) => void

export function mockMatchMedia(matches: boolean): MatchMediaHandle {
  let current = matches
  let media = ''
  const listeners = new Set<ChangeListener>()

  const list = {
    get matches(): boolean {
      return current
    },
    get media(): string {
      return media
    },
    onchange: null,
    addListener: (listener: ChangeListener): void => {
      listeners.add(listener)
    },
    removeListener: (listener: ChangeListener): void => {
      listeners.delete(listener)
    },
    addEventListener: (_type: string, listener: ChangeListener): void => {
      listeners.add(listener)
    },
    removeEventListener: (_type: string, listener: ChangeListener): void => {
      listeners.delete(listener)
    },
    dispatchEvent: (): boolean => true,
  } as unknown as MediaQueryList

  window.matchMedia = vi.fn().mockImplementation((query: string) => {
    media = query
    return list
  })

  return {
    changeMatches(next: boolean): void {
      current = next
      for (const listener of listeners) {
        listener({ matches: next, media })
      }
    },
  }
}
