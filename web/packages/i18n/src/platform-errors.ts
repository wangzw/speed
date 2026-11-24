/**
 * The platform error-copy bundle: `errors.<module>.<code>` leaves held to the
 * writable set by `tools/check_platform_errors.py`. It ships as the
 * ./platform-errors subpath, registered once as the fallback namespace.
 */

import type { i18n as I18nInstance } from 'i18next'
import { readSupportedLanguages } from './languages.js'
import {
  collectLeafPaths,
  registerNamespace,
  type ResourceBundle,
} from './register.js'
import enUS from './platform-errors/locales/en-US.json' with { type: 'json' }
import zhCN from './platform-errors/locales/zh-CN.json' with { type: 'json' }

/** The namespace the platform error bundle registers under. */
export const PLATFORM_ERRORS_NAMESPACE = 'platform-errors' as const

/** The shipped bundles, one per language the package itself carries. */
export const platformErrorResources: Readonly<Record<string, ResourceBundle>> = {
  'zh-CN': zhCN as ResourceBundle,
  'en-US': enUS as ResourceBundle,
}

function listPaths(paths: readonly string[]): string {
  const shown = paths.slice(0, 5).map((path) => `"${path}"`)
  return paths.length <= 5
    ? shown.join(', ')
    : `${shown.join(', ')} and ${paths.length - 5} more`
}

/** Refuses a bundle that does not carry the packaged platform-error key set. */
export function assertPlatformErrorBundleKeySet(
  language: string,
  bundle: ResourceBundle,
): void {
  const reference = platformErrorResources['en-US']
  if (reference === undefined) {
    return
  }
  const referenceLeaves = new Set<string>()
  collectLeafPaths(reference, '', referenceLeaves, PLATFORM_ERRORS_NAMESPACE)
  const leaves = new Set<string>()
  collectLeafPaths(bundle, '', leaves, PLATFORM_ERRORS_NAMESPACE)
  const missingKeys = [...referenceLeaves]
    .filter((path) => !leaves.has(path))
    .sort()
  const addedKeys = [...leaves].filter((path) => !referenceLeaves.has(path)).sort()
  if (missingKeys.length > 0 || addedKeys.length > 0) {
    throw new Error(
      `[speed-i18n] the platform error bundle for "${language}" must carry ` +
        'the packaged key set' +
        (missingKeys.length > 0 ? `; missing ${listPaths(missingKeys)}` : '') +
        (addedKeys.length > 0
          ? `; not in the packaged bundle ${listPaths(addedKeys)}`
          : '') +
        '. The bundle is the lookup table for the platform error codes.',
    )
  }
}

/**
 * Registers the platform error bundle on the instance; `extra` supplies whole
 * languages the package does not carry, each held to the packaged key set.
 */
export function registerPlatformErrors(
  instance: I18nInstance,
  extra: Readonly<Record<string, ResourceBundle>> = {},
): void {
  const supported = readSupportedLanguages(instance)
  if (supported.length === 0) {
    throw new Error(
      '[speed-i18n] registerPlatformErrors needs the instance to pin a ' +
        'supported-language set; create the instance with createI18n (bare ' +
        'i18next instances are refused).',
    )
  }
  const carried = Object.keys(platformErrorResources)
  const extraLanguages = Object.keys(extra)
  const alreadyCarried = extraLanguages.filter((language) =>
    carried.includes(language),
  )
  if (alreadyCarried.length > 0) {
    throw new Error(
      `[speed-i18n] the platform error bundle already ships ` +
        `[${alreadyCarried.join(', ')}]; the extra bundles supply only whole ` +
        `languages the package does not carry ` +
        `[${carried.join(', ')}]. Overlay the exported platformErrorResources ` +
        "to change an existing language's text, then register the result.",
    )
  }
  const unsupported = extraLanguages.filter(
    (language) => !supported.includes(language),
  )
  if (unsupported.length > 0) {
    throw new Error(
      `[speed-i18n] the extra platform error bundles cover language(s) ` +
        `[${unsupported.join(', ')}], which are not among the instance's ` +
        `supported languages [${supported.join(', ')}].`,
    )
  }
  for (const language of extraLanguages) {
    const bundle = extra[language]
    if (bundle === undefined) {
      throw new Error(
        `[speed-i18n] the extra platform error bundle for "${language}" is ` +
          'missing: the extra record names the language without a bundle.',
      )
    }
    assertPlatformErrorBundleKeySet(language, bundle)
  }
  const missing = supported.filter(
    (language) =>
      platformErrorResources[language] === undefined &&
      extra[language] === undefined,
  )
  if (missing.length > 0) {
    throw new Error(
      `[speed-i18n] the platform error bundle has no resources for ` +
        `[${missing.join(', ')}]; the package ships ` +
        `[${carried.join(', ')}]. The bundle is hand-maintained at ` +
        'src/platform-errors/locales/; add its language file there, or pass ' +
        'the whole language through the second argument when the host ' +
        'supplies it.',
    )
  }
  const resources: Record<string, ResourceBundle> = {}
  for (const language of supported) {
    resources[language] = platformErrorResources[language] ?? extra[language]!
  }
  registerNamespace(instance, PLATFORM_ERRORS_NAMESPACE, resources)
}
