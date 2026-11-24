/**
 * User-agent summarization for the session rows of the account surface. The
 * extractor answers only a mapped label, never a fragment of the input, so no
 * piece of the raw string can leak into a row.
 */

const SEPARATOR = ' · '

/** Most specific first; each label is the whole answer, never a substring. */
const BROWSER_MARKERS: ReadonlyArray<readonly [RegExp, string]> = [
  [/Edg(?:e|A|iOS)?\/\d/, 'Edge'],
  [/SamsungBrowser\//, 'Samsung Internet'],
  [/OPR\/|Opera\//, 'Opera'],
  [/FxiOS\/|Firefox\//, 'Firefox'],
  [/Chromium\/|CriOS\/|Chrome\//, 'Chrome'],
  [/curl\//, 'curl'],
  // Safari must come last: a Chromium UA carries its own Safari/ token,
  // so every earlier marker wins over it.
  [/Safari\//, 'Safari'],
]

/**
 * Most specific first: iPhone and iPad embed "like Mac OS X", and Android
 * embeds "(Linux; ...)".
 */
const OS_MARKERS: ReadonlyArray<readonly [RegExp, string]> = [
  [/Windows NT \d/, 'Windows'],
  [/CrOS/, 'ChromeOS'],
  [/\(iPhone;/, 'iPhone'],
  [/\(iPad;/, 'iPad'],
  [/Mac OS X|Macintosh/, 'macOS'],
  [/Android\b/, 'Android'],
  [/Linux\b/, 'Linux'],
]

/**
 * Summarises a raw User-Agent string into the label a session row shows.
 * Answers null when nothing is recognised; the raw string is never a
 * fallback.
 */
export function summarizeUserAgent(ua: string): string | null {
  const parts: string[] = []
  for (const [marker, label] of BROWSER_MARKERS) {
    if (marker.test(ua)) {
      parts.push(label)
      break
    }
  }
  for (const [marker, label] of OS_MARKERS) {
    if (marker.test(ua)) {
      parts.push(label)
      break
    }
  }
  return parts.length > 0 ? parts.join(SEPARATOR) : null
}
