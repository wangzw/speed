/**
 * Language names in a UI language through `Intl.DisplayNames`. A base language
 * subtag is named unless the supported set holds several tags of the same base;
 * every failure falls through to the raw tag.
 */

function baseLanguageOf(tag: string): string {
  try {
    const language = new Intl.Locale(tag).language
    return typeof language === 'string' && language.length > 0 ? language : tag
  } catch {
    return tag
  }
}

function displayNameOf(tag: string, uiLanguage: string): string | undefined {
  try {
    return new Intl.DisplayNames([uiLanguage], { type: 'language' }).of(tag)
  } catch {
    return undefined
  }
}

/** The display name of `tag` in `uiLanguage`; multiple same-base variants are named by full tag. */
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
