/**
 * Language names in a UI language.
 *
 * A language picker labels each supported tag with the language's own name
 * in the interface language the user reads, resolved through
 * `Intl.DisplayNames`. The construction is guarded: an engine without
 * `Intl.DisplayNames`, a structurally legal tag it refuses, or a lookup that
 * answers undefined (a region-less tag it cannot name) falls back to the raw
 * tag, so a label never becomes empty and never throws into a render.
 */
export function languageDisplayName(tag: string, uiLanguage: string): string {
  try {
    return new Intl.DisplayNames([uiLanguage], { type: 'language' }).of(tag) ?? tag
  } catch {
    return tag
  }
}
