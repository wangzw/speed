/**
 * Structural guard: the demo starter's generation scripts are copies of
 * the repository's scripts, and a copy drifts unless something pins it.
 *
 * - orval-nodenext-fixup.mjs is the same file on both sides -- its app leg
 *   is an argument, not a mode -- so the two copies must be byte-identical.
 * - feature-names.mjs differs in exactly one documented region: the demo
 *   copy writes only the application's module augmentation, while the
 *   platform entry writes the package registry (and, given a third path,
 *   the augmentation beside it). The same block also carries each copy's
 *   own attribution (the generator path and the command that reruns it),
 *   which reaches the generated header. Everything outside the
 *   generation-mode block must be byte-identical, and the outputs must
 *   agree on the body while each header names its own copy.
 *
 * The comparison runs over bytes, not imports: importing the scripts would
 * execute argument parsing, and the point is that the copies cannot drift,
 * not that either copy is callable.
 *
 * This suite lives in web/scripts/ so the root vitest leg of `make
 * web-check` (`vitest run eslint-rules/ scripts/`) executes it.
 */
import { execFileSync } from 'node:child_process'
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { afterAll, describe, expect, it } from 'vitest'

const webRoot = join(dirname(fileURLToPath(import.meta.url)), '..')
const repoRoot = join(webRoot, '..')
const platformScripts = join(webRoot, 'scripts')
const starterScripts = join(repoRoot, 'starters', 'demo', 'web', 'scripts')

const FEATURE_NAMES = {
  platform: join(platformScripts, 'feature-names.mjs'),
  starter: join(starterScripts, 'feature-names.mjs'),
}
const ORVAL_FIXUP = {
  platform: join(platformScripts, 'orval-nodenext-fixup.mjs'),
  starter: join(starterScripts, 'orval-nodenext-fixup.mjs'),
}

// The block is documented in both feature-names.mjs copies as the only
// difference between them. The marker lines themselves are shared bytes:
// the comparison below keeps them on the outer sides.
const MODE_START =
  '// --- generation mode (the one documented difference between the two copies) ---'
const MODE_END = '// --- end generation mode ---'

/**
 * Split a feature-names.mjs copy into the shared prefix, the mode block and
 * the shared suffix. Both markers must occur exactly once: a second copy of
 * either would let a second divergence hide between them.
 */
function splitAtGenerationMode(source, label) {
  const start = source.indexOf(MODE_START)
  const startAgain = source.indexOf(MODE_START, start + 1)
  const end = source.indexOf(MODE_END)
  const endAgain = source.indexOf(MODE_END, end + 1)

  expect(start, `${label}: one generation-mode start marker`).toBeGreaterThanOrEqual(0)
  expect(startAgain, `${label}: only one generation-mode start marker`).toBe(-1)
  expect(end, `${label}: one generation-mode end marker`).toBeGreaterThanOrEqual(0)
  expect(endAgain, `${label}: only one generation-mode end marker`).toBe(-1)
  expect(start, `${label}: the mode block starts before it ends`).toBeLessThan(end)

  return {
    before: source.slice(0, start + MODE_START.length),
    mode: source.slice(start + MODE_START.length, end),
    after: source.slice(end),
  }
}

/**
 * Split a generated feature file into its header (the attribution lines the
 * generation mode owns) and the body (everything the two copies must render
 * identically).
 */
function splitGeneratedHeader(source) {
  const separator = source.indexOf('\n\n')
  expect(separator).toBeGreaterThan(0)
  return {
    header: source.slice(0, separator),
    body: source.slice(separator),
  }
}

const platformFeatureNames = splitAtGenerationMode(
  readFileSync(FEATURE_NAMES.platform, 'utf8'),
  'web/scripts/feature-names.mjs',
)
const starterFeatureNames = splitAtGenerationMode(
  readFileSync(FEATURE_NAMES.starter, 'utf8'),
  'starters/demo/web/scripts/feature-names.mjs',
)

const generatedVocabularyDir = mkdtempSync(join(tmpdir(), 'speed-feature-names-'))

afterAll(() => {
  rmSync(generatedVocabularyDir, { recursive: true, force: true })
})

describe('starter generation scripts', () => {
  it('keeps feature-names.mjs byte-identical outside the documented mode block', () => {
    // The shared prefix is the shebang, the module doc and the import; the
    // shared suffix is the vocabulary parsing, validation, rendering and
    // the final writeOutputs call. Both must match to the byte.
    expect(starterFeatureNames.before).toBe(platformFeatureNames.before)
    expect(starterFeatureNames.after).toBe(platformFeatureNames.after)
  })

  it('platform feature-names.mjs writes the package registry and the optional app augmentation', () => {
    expect(platformFeatureNames.mode).toContain(
      'writeFileSync(outputPath, platformBody)',
    )
    expect(platformFeatureNames.mode).toContain(
      'writeFileSync(applicationOutputPath, applicationBody)',
    )
    expect(platformFeatureNames.mode).toContain('applicationOutputPath !== undefined')
  })

  it('starter feature-names.mjs writes only the app augmentation', () => {
    expect(starterFeatureNames.mode).toContain(
      'writeFileSync(applicationOutputPath, applicationBody)',
    )
    // No platform output path exists in the copy-out script: the package's
    // src/features.ts belongs to web/package.json's api:gen.
    expect(starterFeatureNames.mode).not.toContain('outputPath,')
    expect(starterFeatureNames.mode).not.toContain('writeFileSync(outputPath')
    expect(starterFeatureNames.mode).toContain(
      'const [inputPath, applicationOutputPath] = process.argv.slice(2)',
    )
  })

  it('keeps orval-nodenext-fixup.mjs byte-identical on both sides', () => {
    expect(readFileSync(ORVAL_FIXUP.starter).equals(readFileSync(ORVAL_FIXUP.platform))).toBe(
      true,
    )
  })

  it('generates the same app augmentation from both copies', () => {
    const vocabularyPath = join(generatedVocabularyDir, 'features.json')
    writeFileSync(
      vocabularyPath,
      JSON.stringify({
        features: [
          { name: 'audit.admin', owner: 'platform' },
          { name: 'workspace.example', owner: 'application' },
        ],
      }),
    )

    const platformOutput = join(generatedVocabularyDir, 'platform-features.ts')
    const platformAppOutput = join(generatedVocabularyDir, 'platform-app-features.ts')
    const starterAppOutput = join(generatedVocabularyDir, 'starter-app-features.ts')

    // The platform entry takes the platform output and optionally the app
    // output; the starter copy takes only the app output, which is the
    // property this step exists to guarantee: no writable platform path.
    execFileSync(
      process.execPath,
      [FEATURE_NAMES.platform, vocabularyPath, platformOutput, platformAppOutput],
      { stdio: 'pipe' },
    )
    execFileSync(
      process.execPath,
      [FEATURE_NAMES.starter, vocabularyPath, starterAppOutput],
      { stdio: 'pipe' },
    )

    // The bodies are the same; each header names the copy that wrote it (the
    // attribution is part of the generation mode, so the two outputs differ
    // there and nowhere else).
    const starterOutput = splitGeneratedHeader(
      readFileSync(starterAppOutput, 'utf8'),
    )
    const platformOutputBody = splitGeneratedHeader(
      readFileSync(platformAppOutput, 'utf8'),
    )
    expect(starterOutput.body).toBe(platformOutputBody.body)
    expect(starterOutput.header).toMatch(
      /^\/\/ Code generated by scripts\/feature-names\.mjs\. DO NOT EDIT\./,
    )
    expect(platformOutputBody.header).toMatch(
      /^\/\/ Code generated by web\/scripts\/feature-names\.mjs\. DO NOT EDIT\./,
    )

    // The platform artifact the starter must never write stays absent.
    expect(readFileSync(platformOutput, 'utf8')).toContain(
      "  'audit.admin': true",
    )
    expect(readFileSync(starterAppOutput, 'utf8')).not.toContain(
      "'audit.admin'",
    )
    expect(readFileSync(starterAppOutput, 'utf8')).toContain(
      "    'workspace.example': true",
    )
  })
})
