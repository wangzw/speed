/**
 * The front-end build runs where pnpm finds the web workspace.
 *
 * pnpm walks up from the directory `-C` names until it meets a
 * pnpm-workspace.yaml. The case resolves that directory the same way and
 * requires the walk to end inside the repository, at a workspace that lists
 * the demo host, with a filter that selects the host by its package name.
 */
import { existsSync, readFileSync } from 'node:fs'
import { dirname, join, relative, resolve } from 'node:path'

import { describe, expect, it } from 'vitest'

import { FRONT_END_BUILD_ARGS, FRONT_END_PACKAGE } from './front-end-build'
import { REPO_ROOT, WEB_DIR } from './paths'

/** The value that follows `flag` in the argument list, if any. */
function valueOf(args: readonly string[], flag: string): string | undefined {
  const at = args.indexOf(flag)
  return at === -1 ? undefined : args[at + 1]
}

/** The nearest directory at or above `start`, inside the repository, holding a workspace file. */
function workspaceRootAbove(start: string): string | undefined {
  const root = resolve(REPO_ROOT)
  for (let directory = resolve(start); ; directory = dirname(directory)) {
    if (relative(root, directory).startsWith('..')) {
      return undefined
    }
    if (existsSync(join(directory, 'pnpm-workspace.yaml'))) {
      return directory
    }
    if (directory === root) {
      return undefined
    }
  }
}

/** The member globs a pnpm-workspace.yaml lists under `packages:`. */
function workspaceMembers(workspaceRoot: string): string[] {
  const lines = readFileSync(join(workspaceRoot, 'pnpm-workspace.yaml'), 'utf8').split('\n')
  const start = lines.findIndex((line) => line.trim() === 'packages:')
  const members: string[] = []
  for (const line of lines.slice(start + 1)) {
    const entry = /^\s+-\s+(\S+)/.exec(line)
    if (entry?.[1] !== undefined) {
      members.push(entry[1])
    } else if (line.trim() !== '' && !line.trim().startsWith('#')) {
      break
    }
  }
  return members
}

describe('the front-end build command', () => {
  it('runs in a directory whose pnpm workspace lists the demo host', () => {
    const directory = valueOf(FRONT_END_BUILD_ARGS, '-C')
    expect(directory, 'the build names the directory pnpm runs in').toBeDefined()
    const workspaceRoot = workspaceRootAbove(directory ?? '')
    expect(
      workspaceRoot,
      `no pnpm-workspace.yaml at or above ${directory ?? ''} inside the repository`,
    ).toBeDefined()
    const members = workspaceMembers(workspaceRoot ?? '').map((member) =>
      resolve(workspaceRoot ?? '', member),
    )
    expect(members).toContain(resolve(WEB_DIR))
  })

  it('selects the demo host by its package name', () => {
    const manifest = JSON.parse(readFileSync(join(WEB_DIR, 'package.json'), 'utf8')) as {
      name: string
    }
    expect(manifest.name).toBe(FRONT_END_PACKAGE)
    expect(valueOf(FRONT_END_BUILD_ARGS, '--filter')).toBe(FRONT_END_PACKAGE)
    expect(FRONT_END_BUILD_ARGS.at(-1)).toBe('build')
  })
})
