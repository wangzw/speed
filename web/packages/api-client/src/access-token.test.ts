import { describe, expect, it } from 'vitest'
import {
  createMemoryAccessTokenStore,
  type AccessTokenStore,
} from './index'

describe('createMemoryAccessTokenStore', () => {
  it('starts signed out and holds one token until cleared', () => {
    const store = createMemoryAccessTokenStore()
    expect(store.get()).toBeNull()

    store.set('access-token-1')
    expect(store.get()).toBe('access-token-1')

    store.set('access-token-2')
    expect(store.get()).toBe('access-token-2')

    store.set(null)
    expect(store.get()).toBeNull()
  })

  it('gives each store its own memory cell', () => {
    const first = createMemoryAccessTokenStore()
    const second = createMemoryAccessTokenStore()
    first.set('first-token')
    expect(second.get()).toBeNull()
  })

  it('is a plain interface hosts implement (sync get/set, string | null)', () => {
    let hostToken: string | null = 'host-issued-token'
    const hostStore: AccessTokenStore = {
      get(): string | null {
        return hostToken
      },
      set(token: string | null): void {
        hostToken = token
      },
    }
    expect(hostStore.get()).toBe('host-issued-token')
    hostStore.set('host-issued-token-2')
    expect(hostStore.get()).toBe('host-issued-token-2')
  })
})
