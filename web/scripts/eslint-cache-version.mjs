import { createHash } from 'node:crypto'
import { readdirSync, readFileSync } from 'node:fs'
import { basename, join } from 'node:path'

/**
 * Digest of the local rule sources and the workspace lockfile.
 *
 * Every file under `rulesDir`, recursively, whose name does not end with
 * `.test.<extension>` contributes its path relative to `rulesDir` and its
 * content to a sha256, each written length-prefixed, in relative-path order;
 * `lockfilePath` contributes its base name and content last. A change to a
 * rule, a rename or an added file, or a dependency change recorded in the
 * lockfile therefore changes the digest.
 *
 * A symbolic link is refused rather than followed, since the digest would
 * otherwise depend on content named outside `rulesDir`.
 *
 * @param {string} rulesDir Directory holding the local ESLint rules.
 * @param {string} lockfilePath Path to the workspace lockfile.
 * @returns {string} The digest, as lowercase hexadecimal.
 */
export function eslintCacheVersion(rulesDir, lockfilePath) {
  const hash = createHash('sha256')
  for (const relative of ruleFiles(rulesDir)) {
    writeFrame(hash, relative)
    writeFrame(hash, readFileSync(join(rulesDir, relative)))
  }
  writeFrame(hash, basename(lockfilePath))
  writeFrame(hash, readFileSync(lockfilePath))
  return hash.digest('hex')
}

function writeFrame(hash, value) {
  const bytes = Buffer.isBuffer(value) ? value : Buffer.from(value)
  hash.update(`${bytes.length}\0`)
  hash.update(bytes)
}

function ruleFiles(dir, prefix = '') {
  const found = []
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const relative = prefix === '' ? entry.name : `${prefix}/${entry.name}`
    if (entry.isSymbolicLink()) {
      throw new Error(
        `eslint rule source ${join(dir, entry.name)} is a symbolic link`,
      )
    }
    if (entry.isDirectory()) {
      found.push(...ruleFiles(join(dir, entry.name), relative))
    } else if (entry.isFile() && !isTestFile(entry.name)) {
      found.push(relative)
    }
  }
  return found.sort()
}

function isTestFile(name) {
  return /\.test\.[^.]+$/.test(name)
}
