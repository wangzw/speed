import type { DeepPartial } from './types.js'

/**
 * Merge `overrides` into a copy of `base`, recursively, and return the copy.
 * No argument is mutated; `undefined` override values are skipped and every
 * own key of `base` survives in the result.
 */
export function deepMerge<T extends object>(
  base: T,
  ...overrides: Array<DeepPartial<T>>
): T {
  const merged = {} as Record<string, unknown>
  copyInto(merged, base as unknown as Record<string, unknown>)
  for (const override of overrides) {
    mergeInto(merged, override as unknown as Record<string, unknown>)
  }
  return merged as T
}

function isPlainRecord(value: unknown): value is Record<string, unknown> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    return false
  }
  const prototype = Object.getPrototypeOf(value)
  return prototype === Object.prototype || prototype === null
}

// Copies base data verbatim, `undefined` included; the skip-undefined rule
// applies only to the override side in mergeInto.
function copyInto(target: Record<string, unknown>, source: Record<string, unknown>): void {
  for (const key of Object.keys(source)) {
    writeOwn(target, key, source[key])
  }
}

function mergeInto(target: Record<string, unknown>, source: Record<string, unknown>): void {
  for (const key of Object.keys(source)) {
    const value = source[key]
    if (value === undefined) {
      continue
    }
    if (isPlainRecord(value)) {
      const child: Record<string, unknown> = {}
      const existing = getOwn(target, key)
      if (isPlainRecord(existing)) {
        copyInto(child, existing)
      }
      writeOwn(target, key, child)
      mergeInto(child, value)
    } else {
      writeOwn(target, key, value)
    }
  }
}

function getOwn(target: Record<string, unknown>, key: string): unknown | undefined {
  return Object.prototype.hasOwnProperty.call(target, key) ? target[key] : undefined
}

function writeOwn(target: Record<string, unknown>, key: string, value: unknown): void {
  Object.defineProperty(target, key, {
    value,
    writable: true,
    // `__proto__` must stay non-enumerable: an enumerable own data property
    // would be picked up by Object.assign-style copies and reassign the
    // receiving object's prototype.
    enumerable: key !== '__proto__',
    configurable: true,
  })
}
