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

const barePattern = /^import \{[^}]*\} from '\.\/runtime';$/
const fixedPattern = /^import \{[^}]*\} from '\.\/runtime\.js';$/
const specifier = "from './runtime';"
const explicitSpecifier = "from './runtime.js';"

const lines = readFileSync(indexPath, 'utf8').split('\n')
const needsFix = lines.filter((line) => barePattern.test(line))
const alreadyFixed = lines.filter((line) => fixedPattern.test(line))

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
