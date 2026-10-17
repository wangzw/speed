/**
 * Persistence binding for the manually chosen language.
 *
 * The binding lives on the instance itself, under a package-private
 * registered symbol, rather than in a module-level WeakMap. The object a
 * language switch is driven through is not necessarily the object
 * createI18n returned: react-i18next's useTranslation hands components a
 * snapshot wrapper -- a distinct object built from the original's own
 * property descriptors -- so a binding keyed by object identity would be
 * absent exactly where it is needed. An own property travels to that
 * wrapper along with the rest of the instance, and a registered symbol
 * (Symbol.for) is shared even by two copies of this module in one bundle.
 *
 * createI18n resolves the storage (host-injected or the browser's
 * localStorage) and the storage key once and binds them, and switchLanguage
 * writes through the same binding. A binding whose storage is null means
 * "no persistence" -- hosts that must not write storage (embedded contexts,
 * tests) opt out explicitly, and nothing ever falls back to a hidden store.
 */

/** Minimal storage surface; satisfies localStorage and any host test double. */
export interface StorageLike {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
}

/** The storage key the language choice is persisted under. */
export const SPEED_LOCALE_STORAGE_KEY = 'speed.locale'

/** What an instance is bound to: the resolved storage and the key to use. */
export interface InstanceStorage {
  readonly storage: StorageLike | null
  readonly key: string
}

/**
 * The key the binding is carried under. Registered (Symbol.for) rather than
 * created per module: two copies of this package must read the same slot.
 */
const STORAGE_BINDING = Symbol.for('@speed/i18n/storage-binding')

/** Bind resolved storage and key to an instance (called by createI18n). */
export function bindInstanceStorage(
  instance: object,
  storage: StorageLike | null,
  key: string,
): void {
  // Non-enumerable, so the binding stays invisible to code that walks the
  // instance's own properties; the descriptor copy react-i18next builds the
  // useTranslation wrapper from still carries it.
  Object.defineProperty(instance, STORAGE_BINDING, {
    value: Object.freeze({ storage, key }),
    writable: false,
    enumerable: false,
    configurable: true,
  })
}

/**
 * The binding for an instance created by createI18n -- or for a wrapper
 * derived from one, such as the object useTranslation returns -- and null
 * for an instance nothing ever bound.
 */
export function boundInstanceStorage(instance: object): InstanceStorage | null {
  const binding = (instance as { [key: symbol]: unknown })[STORAGE_BINDING]
  if (binding === undefined) {
    return null
  }
  return binding as InstanceStorage
}
