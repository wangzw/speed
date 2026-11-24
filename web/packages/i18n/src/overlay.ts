/**
 * Resource overlays: a host enriching shipped bundles before registration. An
 * existing language is merged leaf by leaf (a missing leaf is an error); a new
 * language is taken whole and validated at registration.
 */

import type { ResourceBundle } from './register.js'

/** Resolved language resources: one bundle per language. */
export type ResourceOverlay = Readonly<Record<string, ResourceBundle>>

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

/** Deep-merges `overlay` into `base` per language, returning fresh resources. */
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
