/**
 * Language names in a UI language.
 *
 * A language picker labels each supported tag with the language's own name
 * in the interface language the user reads, resolved through
 * `Intl.DisplayNames`. Names use the tag's base language subtag (en-US is
 * "English", not "American English") unless the deployment's supported set
 * holds more than one tag of the same base language -- then the regional
 * variants must stay distinguishable, so the full tag is named instead. An
 * absent or empty supported set means no variants.
 *
 * Every step is guarded: an engine without `Intl.Locale` or
 * `Intl.DisplayNames`, a structurally legal tag it refuses, or a lookup that
 * answers undefined (a region-less tag it cannot name) falls through to the
 * next level of the naming chain -- the chosen tag's name, the full tag's
 * name, then the raw tag -- so a label never becomes empty and never throws
 * into a render.
 */

/** The base language subtag of a tag, or the raw tag when `Intl.Locale`
 * cannot parse it. */
function baseLanguageOf(tag: string): string {
  try {
    const language = new Intl.Locale(tag).language
    return typeof language === 'string' && language.length > 0 ? language : tag
  } catch {
    return tag
  }
}

/** The tag's name in the UI language, or undefined when the engine cannot
 * produce one. */
function displayNameOf(tag: string, uiLanguage: string): string | undefined {
  try {
    return new Intl.DisplayNames([uiLanguage], { type: 'language' }).of(tag)
  } catch {
    return undefined
  }
}

export function languageDisplayName(
  tag: string,
  uiLanguage: string,
  supportedLanguages?: readonly string[],
): string {
  const base = baseLanguageOf(tag)
  const variants =
    (supportedLanguages ?? []).filter(
      (supported) => baseLanguageOf(supported) === base,
    ).length > 1
  return (
    displayNameOf(variants ? tag : base, uiLanguage) ??
    displayNameOf(tag, uiLanguage) ??
    tag
  )
}
