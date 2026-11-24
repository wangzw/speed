/**
 * Rewrites orval's extensionless `./runtime` mutator import in the generated
 * API entry to `./runtime.js`, which a nodenext build requires (TS2835). A
 * generated file with neither form is drift: fail loudly.
 */
import { readFileSync, writeFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { resolve } from 'node:path'

const defaultIndex = fileURLToPath(
  new URL('../packages/api-sdk/src/index.ts', import.meta.url),
)
const defaultSeam = fileURLToPath(
  new URL('../packages/api-sdk/src/runtime.ts', import.meta.url),
)
const indexPath = process.argv[2]
  ? resolve(process.cwd(), process.argv[2])
  : defaultIndex
const seamPath = process.argv[3]
  ? resolve(process.cwd(), process.argv[3])
  : defaultSeam

// One mutator import per line, braces between: orval emits a distinct
// line per mutator name, all from './runtime'. The specifier -- not the
// imported names -- is what nodenext rejects, so the rewrite replaces
// only the specifier and never needs to know which mutators exist.
// The two forms are disjoint (the '.js' specifier cannot match the
// bare pattern and vice versa), so the needs-fix and already-fixed
// counts below partition the file's mutator imports cleanly.
const barePattern = /^import \{[^}]*\} from '\.\/runtime';$/
const fixedPattern = /^import \{[^}]*\} from '\.\/runtime\.js';$/
const specifier = "from './runtime';"
const explicitSpecifier = "from './runtime.js';"

const lines = readFileSync(indexPath, 'utf8').split('\n')
const needsFix = lines.filter((line) => barePattern.test(line))
const alreadyFixed = lines.filter((line) => fixedPattern.test(line))

// No mutator import in EITHER form is the drift condition: a
// regeneration that ships no './runtime' import at all cannot be a
// file this script already fixed (that state always carries the
// explicit '.js' form), so there is nothing to rewrite and nothing to
// confirm -- fail loudly. An already-fixed file is NOT drift.
if (needsFix.length === 0 && alreadyFixed.length === 0) {
  console.error(
    `[orval-nodenext-fixup] expected at least one './runtime' or './runtime.js' mutator import in ${indexPath}, found none. ` +
      'orval emission changed (version drift?) -- regenerate with the pinned orval and re-run, then commit the artifact.',
  )
  process.exit(1)
}

let seamOk = true
try {
  readFileSync(seamPath, 'utf8')
} catch {
  seamOk = false
}
if (!seamOk) {
  console.error(
    `[orval-nodenext-fixup] the rewritten import points at ${seamPath}, which does not exist.`,
  )
  process.exit(1)
}

if (needsFix.length > 0) {
  const rewritten = lines.map((line) =>
    barePattern.test(line)
      ? line.replace(specifier, explicitSpecifier)
      : line,
  )
  const result = rewritten.join('\n')
  if (result !== lines.join('\n')) {
    writeFileSync(indexPath, result)
  }
  console.log(
    `[orval-nodenext-fixup] rewrote ${needsFix.length} mutator import(s) to './runtime.js' in ${indexPath}`,
  )
} else {
  console.log(
    `[orval-nodenext-fixup] mutator imports already carry the .js extension in ${indexPath}`,
  )
}
