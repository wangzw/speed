/**
 * Namespace registration: one namespace per package, registered once per instance
 * and validated completely before anything lands (coverage, identical leaf sets,
 * non-empty leaves, plural coverage); a failure while adding rolls back.
 */

const PLURAL_CATEGORIES = ['zero', 'one', 'two', 'few', 'many', 'other'] as const

const PLURAL_SUFFIXES = PLURAL_CATEGORIES.map((category) => `_${category}`)

interface PluralFamily {
  readonly path: string
  readonly forms: string[]
}

import type { i18n as I18nInstance } from 'i18next'
import { readSupportedLanguages } from './languages.js'

/** A translation bundle: nested string leaves under plain records. */
export interface ResourceBundle {
  readonly [segment: string]: string | ResourceBundle
}

const NAMESPACE_PATTERN = /^[A-Za-z][A-Za-z0-9_-]*$/

/**
 * Namespaces already registered per instance, carried as an own property under
 * a registered symbol so a descriptor-copying wrapper and a second module copy
 * both see it.
 */
const REGISTERED_NAMESPACES = Symbol.for('@speed/i18n/registered-namespaces')

function registeredNamespacesOf(instance: I18nInstance): Set<string> | undefined {
  return (instance as unknown as Record<symbol, Set<string> | undefined>)[REGISTERED_NAMESPACES]
}

function markNamespaces(instance: I18nInstance, namespaces: Set<string>): void {
  Object.defineProperty(instance, REGISTERED_NAMESPACES, {
    value: namespaces,
    enumerable: false,
    configurable: true,
    writable: true,
  })
}

function describeLeaf(value: unknown): string {
  if (value === null) {
    return 'null'
  }
  if (Array.isArray(value)) {
    return 'an array'
  }
  return `a ${typeof value}`
}

export function collectLeafPaths(
  bundle: ResourceBundle,
  prefix: string,
  leaves: Set<string>,
  namespace: string,
): void {
  for (const [key, value] of Object.entries(bundle)) {
    const path = prefix === '' ? key : `${prefix}.${key}`
    if (typeof value === 'string') {
      if (value === '') {
        throw new Error(
          `[speed-i18n] namespace "${namespace}": the "${path}" translation is ` +
            'empty: an empty string renders as silence and never fires the ' +
            'missing-key discipline, so a key reduced to "" is indistinguishable ' +
            'at runtime from a dropped translation. Ship real text in every ' +
            'language or remove the key.',
        )
      }
      leaves.add(path)
      continue
    }
    if (typeof value !== 'object' || value === null || Array.isArray(value)) {
      throw new Error(
        `[speed-i18n] namespace "${namespace}": translation values must be strings or ` +
          `nested objects; "${path}" is ${describeLeaf(value)}.`,
      )
    }
    collectLeafPaths(value as ResourceBundle, path, leaves, namespace)
  }
}

function pluralCategoriesOf(language: string): string[] {
  let selectable: readonly string[]
  try {
    selectable = new Intl.PluralRules(language).resolvedOptions().pluralCategories
  } catch {
    throw new Error(
      `[speed-i18n] cannot resolve the plural categories of language ` +
        `"${language}": Intl.PluralRules refused the tag, so plural-form ` +
        'validation cannot run for it.',
    )
  }
  const selectableSet = new Set(selectable)
  return PLURAL_CATEGORIES.filter((category) => selectableSet.has(category))
}

function pluralFamiliesOf(paths: Set<string>): PluralFamily[] {
  const formsByFamily = new Map<string, string[]>()
  for (const path of paths) {
    const leafName = path.slice(path.lastIndexOf('.') + 1)
    for (const suffix of PLURAL_SUFFIXES) {
      // A form names a family, never itself: the leaf must be longer than the suffix.
      if (leafName.endsWith(suffix) && leafName.length > suffix.length) {
        const familyPath = path.slice(0, path.length - suffix.length)
        let forms = formsByFamily.get(familyPath)
        if (forms === undefined) {
          forms = []
          formsByFamily.set(familyPath, forms)
        }
        forms.push(suffix.slice(1))
        break
      }
    }
  }
  const byCategoryOrder = (a: string, b: string): number =>
    PLURAL_CATEGORIES.findIndex((category) => category === a) -
    PLURAL_CATEGORIES.findIndex((category) => category === b)
  for (const forms of formsByFamily.values()) {
    forms.sort(byCategoryOrder)
  }
  return [...formsByFamily.entries()]
    .map(([path, forms]) => ({ path, forms }))
    .sort((a, b) => (a.path < b.path ? -1 : a.path > b.path ? 1 : 0))
}

function listSample(paths: Set<string>): string {
  const sorted = [...paths].sort()
  if (sorted.length <= 5) {
    return sorted.map((path) => `"${path}"`).join(', ')
  }
  const shown = sorted.slice(0, 5).map((path) => `"${path}"`)
  return `${shown.join(', ')} and ${sorted.length - 5} more`
}

/**
 * Registers a package's translations under a namespace. Refuses, before mutating
 * anything, an unsupported or missing language, mismatched leaf sets, empty
 * leaves, incomplete plural families, a bad name or a second registration.
 */
export function registerNamespace(
  instance: I18nInstance,
  namespace: string,
  resources: Readonly<Record<string, ResourceBundle>>,
): void {
  if (!NAMESPACE_PATTERN.test(namespace)) {
    throw new Error(
      `[speed-i18n] namespace "${namespace}" is not a valid namespace: names must ` +
        "start with a letter and contain only letters, digits, '_' and '-'.",
    )
  }
  const registered = registeredNamespacesOf(instance)
  if (registered !== undefined && registered.has(namespace)) {
    throw new Error(
      `[speed-i18n] namespace "${namespace}" is already registered on this instance. ` +
        'A namespace registers exactly once per instance; double registration usually ' +
        'means a module was initialized twice (tests, SSR).',
    )
  }
  const supported = readSupportedLanguages(instance)
  if (supported.length === 0) {
    throw new Error(
      '[speed-i18n] registerNamespace needs the instance to pin a supported-language ' +
        'set; create the instance with createI18n (bare i18next instances are refused).',
    )
  }
  const languageKeys = Object.keys(resources)
  if (languageKeys.length === 0) {
    throw new Error(
      `[speed-i18n] namespace "${namespace}": resources must provide at least one language.`,
    )
  }
  const unsupported = languageKeys.filter(
    (language) => !supported.includes(language),
  )
  if (unsupported.length > 0) {
    throw new Error(
      `[speed-i18n] namespace "${namespace}": resource language(s) ` +
        `[${unsupported.join(', ')}] are not among the instance's supported languages ` +
        `[${supported.join(', ')}]; use canonical tags (e.g. "zh-CN"), one bundle per ` +
        'supported language.',
    )
  }
  const missing = supported.filter((language) => !languageKeys.includes(language))
  if (missing.length > 0) {
    throw new Error(
      `[speed-i18n] namespace "${namespace}" must cover every supported language; ` +
        `missing bundle(s) for [${missing.join(', ')}]. Each language's bundle must ` +
        'ship in the same registration.',
    )
  }

  const leafSets = new Map<string, Set<string>>()
  for (const language of languageKeys) {
    const bundle = resources[language]!
    const leaves = new Set<string>()
    collectLeafPaths(bundle, '', leaves, namespace)
    if (leaves.size === 0) {
      throw new Error(
        `[speed-i18n] namespace "${namespace}": the "${language}" bundle ships no ` +
        'translation keys.',
      )
    }
    leafSets.set(language, leaves)
  }

  const referenceLanguage = languageKeys[0]!
  const reference = leafSets.get(referenceLanguage)!
  for (const language of languageKeys) {
    const leaves = leafSets.get(language)!
    const missingKeys = [...reference].filter((path) => !leaves.has(path))
    const extraKeys = [...leaves].filter((path) => !reference.has(path))
    if (missingKeys.length > 0 || extraKeys.length > 0) {
      const parts: string[] = []
      if (missingKeys.length > 0) {
        parts.push(
          `missing in "${language}" relative to "${referenceLanguage}": ` +
            listSample(new Set(missingKeys)),
        )
      }
      if (extraKeys.length > 0) {
        parts.push(
          `present in "${language}" but not in "${referenceLanguage}": ` +
            listSample(new Set(extraKeys)),
        )
      }
      throw new Error(
        `[speed-i18n] namespace "${namespace}": the language bundles must carry the ` +
          `same key set; ${parts.join('; ')}.`,
      )
    }
  }

  // Identical leaf sets mean one family set serves all languages; it must cover every count category each can select.
  const families = pluralFamiliesOf(leafSets.get(referenceLanguage)!)
  if (families.length > 0) {
    const selectableByLanguage = new Map(
      languageKeys.map((language) => [language, pluralCategoriesOf(language)]),
    )
    const parts: string[] = []
    for (const family of families) {
      const shipped = new Set(family.forms)
      const gaps: string[] = []
      for (const language of languageKeys) {
        const missing = selectableByLanguage
          .get(language)!
          .filter((category) => !shipped.has(category))
        if (missing.length > 0) {
          gaps.push(
            `counts in "${language}" can fall into [${missing.join(', ')}], which ` +
              'the family does not ship',
          )
        }
      }
      if (gaps.length > 0) {
        parts.push(
          `plural key family "${family.path}" is incomplete: ${gaps.join('; ')}; ` +
            `add the missing "${family.path}_<category>" leaves to every ` +
            "language's bundle (a count whose form is absent renders the raw key)",
        )
      }
    }
    if (parts.length > 0) {
      throw new Error(
        `[speed-i18n] namespace "${namespace}": ${parts.join('; ')}. Plural forms ` +
          `are suffixed leaves ("${families[0]!.path}_one", "${families[0]!.path}_other", ...) ` +
          'resolved per count, and bundles carry identical leaf sets -- so a form one ' +
          "language needs ships in every language's bundle, including languages that " +
          'never select it themselves.',
      )
    }
  }

  try {
    for (const language of languageKeys) {
      instance.addResourceBundle(language, namespace, resources[language]!, true, true)
    }
  } catch (error) {
    for (const language of languageKeys) {
      if (instance.hasResourceBundle(language, namespace)) {
        instance.removeResourceBundle(language, namespace)
      }
    }
    throw error
  }

  const instanceNamespaces = registered ?? new Set<string>()
  instanceNamespaces.add(namespace)
  markNamespaces(instance, instanceNamespaces)
}
