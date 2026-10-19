/**
 * The platform error-copy bundle and its registration.
 *
 * The bundle is the client-side text for the apperr codes the earlier
 * implementation published a catalog entry for; it is hand-maintained now
 * (its generator retired with that implementation), and backend-only
 * content (invitation emails, notification templates, SMS bodies, seed
 * copy) has no code to be looked up with and is therefore absent by
 * construction. Keys are the `errors` section the packages'
 * resolvers already navigate, one flat dotted apperr code per leaf
 * (`errors.authn.invalid_credentials`), so `t('errors.' + code)` resolves
 * through i18next's own deepFind without a lookup helper.
 *
 * Isolated in its own subpath so the main entry's export table stays
 * exactly as documented (the ./mui-locale precedent: a consumer that
 * never resolves a backend code never pulls this bundle). A host
 * registers it once at bootstrap and names it as the instance's fallback
 * namespace, which is what lets a code a package's own namespace does
 * not cover resolve to the module catalog's words instead of the raw
 * key:
 *
 *   const i18n = createI18n({ fallbackNamespaces: [PLATFORM_ERRORS_NAMESPACE] })
 *   registerPlatformErrors(i18n)
 *
 * A host whose instance supports a language the package does not ship
 * passes that whole language's bundle as the second argument; the
 * exported `platformErrorResources` is the shipped pair, for a host that
 * overlays it (see `overlayResources`) before registering.
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

/** A short, deterministic leaf-path listing for a failure message. */
function listPaths(paths: readonly string[]): string {
  const shown = paths.slice(0, 5).map((path) => `"${path}"`)
  return paths.length <= 5
    ? shown.join(', ')
    : `${shown.join(', ')} and ${paths.length - 5} more`
}

/**
 * Register the platform error bundle on the instance.
 *
 * Registration goes through registerNamespace, so the bundle's
 * validation is the package's own: every language present, identical leaf
 * sets, non-empty strings. `extra` supplies whole languages the package
 * does not carry: an instance whose supported set is wider than the
 * shipped pair registers only when every supported language is supplied.
 * An extra bundle must carry the same key set as the packaged bundle: it
 * is compared against the packaged reference directly (so an instance whose
 * supported set omits the packaged pair is still checked), and the
 * registration parity check then enforces the same key set across every
 * registered language. An extra entry for a language the package already
 * carries is refused, because whole-language supply and per-leaf override
 * are different operations (a host changing an existing language's text
 * overlays the exported resources instead).
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
  // A supplied bundle is always compared to the packaged reference, not
  // only to the other registered languages: an instance whose supported
  // set omits the packaged pair still needs the whole code table.
  const reference = platformErrorResources['en-US']
  if (reference !== undefined) {
    const referenceLeaves = new Set<string>()
    collectLeafPaths(reference, '', referenceLeaves, PLATFORM_ERRORS_NAMESPACE)
    for (const language of extraLanguages) {
      const bundle = extra[language]
      if (bundle === undefined) {
        throw new Error(
          `[speed-i18n] the extra platform error bundle for "${language}" is ` +
            'missing: the extra record names the language without a bundle.',
        )
      }
      const leaves = new Set<string>()
      collectLeafPaths(bundle, '', leaves, PLATFORM_ERRORS_NAMESPACE)
      const missingKeys = [...referenceLeaves]
        .filter((path) => !leaves.has(path))
        .sort()
      const addedKeys = [...leaves]
        .filter((path) => !referenceLeaves.has(path))
        .sort()
      if (missingKeys.length > 0 || addedKeys.length > 0) {
        throw new Error(
          `[speed-i18n] the extra platform error bundle for "${language}" must ` +
            'carry the packaged key set' +
            (missingKeys.length > 0
              ? `; missing ${listPaths(missingKeys)}`
              : '') +
            (addedKeys.length > 0
              ? `; not in the packaged bundle ${listPaths(addedKeys)}`
              : '') +
            '. The bundle is the lookup table for the platform error codes.',
        )
      }
    }
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
