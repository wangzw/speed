/**
 * Resource overlays: a host enriching a package's shipped bundles before
 * registration.
 *
 * The overlay maps language tags to partial bundles. An overlay for a
 * language the base bundle carries is deep-merged into it: a leaf that
 * exists in the base is replaced, and a leaf that does not exist is an
 * error -- an overlay written against an older key set would otherwise
 * silently add a key the rest of the languages do not carry, and an
 * upgrade that dropped a key would leave the overlay pointing at nothing.
 * A language the base does not carry is taken whole (a host supplying a
 * language the package does not ship); its completeness -- every key and
 * every plural form the language needs -- is validated later by
 * `registerNamespace`, together with the rest of the namespace.
 *
 * The base and the overlay are never mutated: the result is fresh, so a
 * package's exported resources stay exactly what it shipped.
 */

import type { ResourceBundle } from './register.js'

/** Resolved language resources: one bundle per language. */
export type ResourceOverlay = Readonly<Record<string, ResourceBundle>>

/** A bundle being built: the writable counterpart of ResourceBundle. */
interface MutableBundle {
  [segment: string]: string | MutableBundle
}

function isPlainRecord(value: unknown): value is ResourceBundle {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function describe(value: unknown): string {
  if (value === null) {
    return 'null'
  }
  if (Array.isArray(value)) {
    return 'an array'
  }
  return typeof value === 'object' ? 'an object' : `a ${typeof value}`
}

/**
 * A deep copy of a bundle, refusing anything that is neither a string leaf
 * nor a plain record. The overlay path never introduces a new leaf into an
 * existing bundle, but a whole new language arrives as-is and is copied
 * with the same shape check registration would apply.
 */
function cloneBundle(
  language: string,
  bundle: ResourceBundle,
  prefix: string,
): MutableBundle {
  const copy: MutableBundle = {}
  for (const [key, value] of Object.entries(bundle)) {
    const path = prefix === '' ? key : `${prefix}.${key}`
    if (typeof value === 'string') {
      copy[key] = value
      continue
    }
    if (!isPlainRecord(value)) {
      throw new Error(
        `[speed-i18n] resource overlay (${language}): translation values must be ` +
          `strings or nested objects; "${path}" is ${describe(value)}.`,
      )
    }
    copy[key] = cloneBundle(language, value, path)
  }
  return copy
}

/**
 * Merges one overlay bundle into one base bundle, in place on `target` (a
 * fresh clone of the base). Every leaf the overlay names must already
 * exist as a leaf; a subtree the overlay introduces must already exist as
 * a record.
 */
function mergeBundle(
  language: string,
  target: MutableBundle,
  overlay: ResourceBundle,
  prefix: string,
): void {
  for (const [key, value] of Object.entries(overlay)) {
    const path = prefix === '' ? key : `${prefix}.${key}`
    const current = target[key]
    if (typeof value === 'string') {
      if (typeof current !== 'string') {
        throw new Error(
          `[speed-i18n] resource overlay (${language}) cannot replace "${path}": ` +
            `the base bundle has no such leaf` +
            (current === undefined ? '' : ` (it is ${describe(current)})`) +
            '. A leaf an overlay names must exist before it can be replaced; ' +
            'deleting or renaming a key is incompatible, so the package ' +
            'version the overlay was written against no longer matches this one.',
        )
      }
      target[key] = value
      continue
    }
    if (!isPlainRecord(value)) {
      throw new Error(
        `[speed-i18n] resource overlay (${language}): translation values must be ` +
          `strings or nested objects; "${path}" is ${describe(value)}.`,
      )
    }
    if (!isPlainRecord(current)) {
      throw new Error(
        `[speed-i18n] resource overlay (${language}) cannot add the subtree ` +
          `"${path}": the base bundle has no such subtree, so every leaf under ` +
          'it would be a new key.',
      )
    }
    mergeBundle(language, current, value, path)
  }
}

/**
 * Deep-merges `overlay` into `base` per language, returning fresh
 * resources. Replacing a leaf the base does not carry throws; adding a
 * whole new language is allowed and validated at registration.
 */
export function overlayResources(
  base: ResourceOverlay,
  overlay: ResourceOverlay,
): Record<string, ResourceBundle> {
  const merged: Record<string, MutableBundle> = {}
  for (const [language, bundle] of Object.entries(base)) {
    merged[language] = cloneBundle(language, bundle, '')
  }
  for (const [language, bundle] of Object.entries(overlay)) {
    const target = merged[language]
    if (target === undefined) {
      merged[language] = cloneBundle(language, bundle, '')
      continue
    }
    mergeBundle(language, target, bundle, '')
  }
  return merged
}
