/** Everything the handler knows about one missing translation key. */
export interface MissingKeyDetails {
  /** Canonical language tags the lookup ran for. */
  readonly languages: readonly string[]
  /** The namespace the key was looked up in. */
  readonly namespace: string
  /** The key as requested, dot-joined when nested. */
  readonly key: string
}

/** Default handler: a console warning, prefixed so it is greppable. */
export function defaultMissingKeyHandler(details: MissingKeyDetails): void {
  const { languages, namespace, key } = details
  console.warn(
    `[speed-i18n] missing translation key "${key}" in namespace "${namespace}" ` +
      `for language(s): ${languages.join(', ')}; no fallback to another language is ` +
      'performed and the key is rendered as-is. Add the key to every language ' +
      'resource of the namespace.',
  )
}

export function missingKeyHandlerFactory(
  onMissingKey?: (details: MissingKeyDetails) => void,
): (lngs: readonly string[], namespace: string, key: string) => void {
  const handle = onMissingKey ?? defaultMissingKeyHandler
  return (lngs, namespace, key): void => {
    handle({ languages: lngs, namespace, key })
  }
}
