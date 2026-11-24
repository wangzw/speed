/**
 * Persistence binding for the manually chosen language. The binding is an own
 * property under a registered symbol on the instance.
 */

/** Minimal storage surface; satisfies localStorage and any host test double. */
export interface StorageLike {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
}

/** The storage key the language choice is persisted under. */
export const SPEED_LOCALE_STORAGE_KEY = 'speed.locale'

export interface InstanceStorage {
  readonly storage: StorageLike | null
  readonly key: string
}

const STORAGE_BINDING = Symbol.for('@speed/i18n/storage-binding')

export function bindInstanceStorage(
  instance: object,
  storage: StorageLike | null,
  key: string,
): void {
  // Non-enumerable, so walking the instance's own properties does not see it; the descriptor copy still carries it.
  Object.defineProperty(instance, STORAGE_BINDING, {
    value: Object.freeze({ storage, key }),
    writable: false,
    enumerable: false,
    configurable: true,
  })
}

export function boundInstanceStorage(instance: object): InstanceStorage | null {
  const binding = (instance as { [key: symbol]: unknown })[STORAGE_BINDING]
  if (binding === undefined) {
    return null
  }
  return binding as InstanceStorage
}
