/**
 * Run one tsc program per named config, in order.
 *
 * Usage: typecheck-each.mjs <config>... [tsc flags...]
 *
 * Everything before the first `-`-prefixed argument names a config; that
 * argument and every one after it go to each `tsc -p <config>` unchanged.
 * The run stops at the first non-zero exit and exits with that code, the
 * same result `tsc -p a && tsc -p b` gave.
 *
 * tsc is resolved from PATH, where pnpm puts the member's
 * node_modules/.bin, and is started without a shell.
 */
import { spawnSync } from 'node:child_process'
import { constants } from 'node:os'

const argv = process.argv.slice(2)
const flagStart = argv.findIndex((arg) => arg.startsWith('-'))
const configs = flagStart === -1 ? argv : argv.slice(0, flagStart)
const forwarded = flagStart === -1 ? [] : argv.slice(flagStart)

if (configs.length === 0) {
  console.error('typecheck-each: name at least one tsconfig before the flags')
  process.exit(2)
}

for (const config of configs) {
  const result = spawnSync('tsc', ['-p', config, ...forwarded], {
    stdio: 'inherit',
    shell: false,
  })
  if (result.error) {
    console.error(`typecheck-each: could not run tsc: ${result.error.message}`)
    process.exit(1)
  }
  if (result.status === null) {
    // tsc died on a signal; report it the way a shell reports a signalled
    // command: 128 plus the signal number.
    process.exit(128 + (constants.signals[result.signal] ?? 0))
  }
  if (result.status !== 0) {
    process.exit(result.status)
  }
}
