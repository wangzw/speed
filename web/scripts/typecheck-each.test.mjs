/**
 * Pins the forwarder's contract: five cases run real tsc against both
 * programs of a sandbox, the signal case runs a stand-in that kills itself,
 * and the last block is structural -- every workspace member with a browser
 * program must go through the forwarder, and no member's typecheck may carry
 * a shell separator that would hide a tsc from the appended flags.
 */
import { spawnSync } from 'node:child_process'
import {
  chmodSync,
  existsSync,
  globSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from 'node:fs'
import { constants, tmpdir } from 'node:os'
import { delimiter, dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { afterAll, describe, expect, it } from 'vitest'

const webRoot = join(dirname(fileURLToPath(import.meta.url)), '..')
const script = join(webRoot, 'scripts', 'typecheck-each.mjs')

// The member's own node_modules/.bin is what pnpm puts on PATH for a script;
// a test process started some other way supplies the same directory.
const toolBin = join(webRoot, 'node_modules', '.bin')

const sandboxes = []

function sandbox(label) {
  const dir = mkdtempSync(join(tmpdir(), `typecheck-each-${label}-`))
  sandboxes.push(dir)
  return dir
}

afterAll(() => {
  for (const dir of sandboxes) {
    rmSync(dir, { recursive: true, force: true })
  }
})

function write(path, contents) {
  mkdirSync(dirname(path), { recursive: true })
  writeFileSync(path, contents)
}

function config(files, options = {}) {
  return JSON.stringify({
    compilerOptions: {
      target: 'ES2022',
      module: 'ESNext',
      moduleResolution: 'bundler',
      noEmit: true,
      strict: true,
      ...options,
    },
    include: files,
  })
}

/** Run the forwarder as a package script would: node, then its arguments. */
function typecheck(cwd, args, bin = toolBin) {
  const result = spawnSync(process.execPath, [script, ...args], {
    cwd,
    encoding: 'utf8',
    env: {
      ...process.env,
      PATH: [bin, process.env.PATH ?? ''].join(delimiter),
    },
  })
  return { status: result.status, output: `${result.stdout}${result.stderr}` }
}

/** Run tsc directly, for the exit code the forwarder must repeat. */
function tsc(cwd, args) {
  const result = spawnSync('tsc', args, {
    cwd,
    encoding: 'utf8',
    env: {
      ...process.env,
      PATH: [toolBin, process.env.PATH ?? ''].join(delimiter),
    },
  })
  return { status: result.status, output: `${result.stdout}${result.stderr}` }
}

describe('typecheck forwarding', () => {
  it('passes an appended flag to every config it checks', () => {
    const dir = sandbox('forward')
    write(join(dir, 'a.ts'), 'export const a: number = 1\n')
    write(join(dir, 'b.ts'), 'export const b: string = "b"\n')
    write(join(dir, 'tsconfig.json'), config(['a.ts']))
    write(join(dir, 'tsconfig.browser.json'), config(['b.ts']))

    const run = typecheck(dir, [
      'tsconfig.json',
      'tsconfig.browser.json',
      '--incremental',
    ])

    expect(run.status, run.output).toBe(0)
    expect(existsSync(join(dir, 'tsconfig.tsbuildinfo'))).toBe(true)
    expect(existsSync(join(dir, 'tsconfig.browser.tsbuildinfo'))).toBe(true)
  })

  it('leaves both programs cold when no flag is appended', () => {
    const dir = sandbox('cold')
    write(join(dir, 'a.ts'), 'export const a: number = 1\n')
    write(join(dir, 'b.ts'), 'export const b: string = "b"\n')
    write(join(dir, 'tsconfig.json'), config(['a.ts']))
    write(join(dir, 'tsconfig.browser.json'), config(['b.ts']))

    const run = typecheck(dir, ['tsconfig.json', 'tsconfig.browser.json'])

    expect(run.status, run.output).toBe(0)
    expect(existsSync(join(dir, 'tsconfig.tsbuildinfo'))).toBe(false)
    expect(existsSync(join(dir, 'tsconfig.browser.tsbuildinfo'))).toBe(false)
  })

  it('stops at the first failing config, as `&&` did', () => {
    const dir = sandbox('short-circuit')
    write(join(dir, 'a.ts'), 'export const a: number = "not a number"\n')
    write(join(dir, 'b.ts'), 'export const b: string = "b"\n')
    write(join(dir, 'tsconfig.json'), config(['a.ts']))
    write(join(dir, 'tsconfig.browser.json'), config(['b.ts']))

    const run = typecheck(dir, [
      'tsconfig.json',
      'tsconfig.browser.json',
      '--incremental',
    ])

    expect(run.status).not.toBe(0)
    const direct = tsc(dir, ['-p', 'tsconfig.json'])
    expect(run.status, 'the forwarder repeats tsc\'s exit code').toBe(direct.status)
    // A missing script would fail without a diagnostic; this pins that tsc
    // ran and reported the first program's error.
    expect(run.output).toMatch(/error TS2322/)
    expect(existsSync(join(dir, 'tsconfig.browser.tsbuildinfo'))).toBe(false)
  })

  it('fails when a later config fails after the first passed', () => {
    const dir = sandbox('later-failure')
    write(join(dir, 'a.ts'), 'export const a: number = 1\n')
    write(join(dir, 'b.ts'), 'export const b: string = 1\n')
    write(join(dir, 'tsconfig.json'), config(['a.ts']))
    write(join(dir, 'tsconfig.browser.json'), config(['b.ts']))

    const run = typecheck(dir, [
      'tsconfig.json',
      'tsconfig.browser.json',
      '--incremental',
    ])

    expect(run.status).not.toBe(0)
    expect(run.output).toMatch(/error TS2322/)
  })

  it('reports a signal-killed tsc with the shell exit code', () => {
    const dir = sandbox('signal')
    const bin = join(dir, 'bin')
    write(join(bin, 'tsc'), '#!/bin/sh\nkill -TERM "$$"\n')
    chmodSync(join(bin, 'tsc'), 0o755)
    write(join(dir, 'a.ts'), 'export const a: number = 1\n')
    write(join(dir, 'tsconfig.json'), config(['a.ts']))

    const run = typecheck(dir, ['tsconfig.json'], bin)

    expect(run.status).toBe(128 + constants.signals.SIGTERM)
  })

  it('invalidates a downstream program when an upstream type changes', () => {
    const dir = sandbox('cross-package')
    const upstream = join(dir, 'upstream')
    const downstream = join(dir, 'downstream')
    write(join(upstream, 'src', 'index.ts'), 'export type Tone = "a" | "b"\n')
    write(join(upstream, 'tsconfig.json'), config(['src']))
    write(
      join(downstream, 'src', 'index.ts'),
      'import type { Tone } from "@speed/tokens"\nexport const tone: Tone = "a"\n',
    )
    write(
      join(downstream, 'tsconfig.json'),
      config(['src'], { paths: { '@speed/tokens': ['../upstream/src/index.ts'] } }),
    )

    const first = typecheck(dir, ['downstream/tsconfig.json', '--incremental'])
    expect(first.status, first.output).toBe(0)
    expect(
      existsSync(join(downstream, 'tsconfig.tsbuildinfo')),
      'the first run left build info, so the next downstream run is hot',
    ).toBe(true)

    // The upstream program stays valid; only the downstream use breaks.
    write(join(upstream, 'src', 'index.ts'), 'export type Tone = "b" | "c"\n')
    const upstreamRun = typecheck(dir, ['upstream/tsconfig.json'])
    expect(upstreamRun.status, upstreamRun.output).toBe(0)

    const hotFailure = typecheck(dir, [
      'downstream/tsconfig.json',
      '--incremental',
    ])
    expect(hotFailure.status).not.toBe(0)
    expect(hotFailure.output).toMatch(/error TS2322/)

    write(join(upstream, 'src', 'index.ts'), 'export type Tone = "a" | "b"\n')
    const restored = typecheck(dir, [
      'downstream/tsconfig.json',
      '--incremental',
    ])
    expect(restored.status, restored.output).toBe(0)
  })
})

describe('the workspace typecheck scripts', () => {
  function workspaceMembers() {
    const lines = readFileSync(join(webRoot, 'pnpm-workspace.yaml'), 'utf8').split('\n')
    const start = lines.findIndex((line) => line.trim() === 'packages:')
    expect(start, 'pnpm-workspace.yaml has a packages: section').toBeGreaterThanOrEqual(0)

    const globs = []
    for (const line of lines.slice(start + 1)) {
      const entry = /^\s+-\s+(\S+)/.exec(line)
      if (entry) {
        globs.push(entry[1])
      } else if (line.trim() && !line.trim().startsWith('#')) {
        break
      }
    }

    return globs
      .flatMap((pattern) => globSync(resolve(webRoot, pattern)))
      .filter((member) => existsSync(join(member, 'package.json')))
  }

  it('routes every browser program through the forwarder', () => {
    const members = workspaceMembers()
    expect(members.length).toBeGreaterThan(0)
    expect(
      members,
      'the workspace list reaches the demo host',
    ).toContain(resolve(webRoot, '../starters/demo/web'))

    const withBrowser = []
    for (const member of members) {
      const { scripts } = JSON.parse(readFileSync(join(member, 'package.json'), 'utf8'))
      const typecheckScript = scripts?.typecheck
      expect(typecheckScript, `${member} declares typecheck`).toBeTypeOf('string')
      expect(
        typecheckScript,
        `${member} typecheck has no shell separator around tsc`,
      ).not.toMatch(/[;&|]/)

      if (!existsSync(join(member, 'tsconfig.browser.json'))) {
        continue
      }
      withBrowser.push(member)

      const words = typecheckScript.split(/\s+/)
      expect(words[0], `${member} runs node`).toBe('node')
      expect(resolve(member, words[1]), `${member} runs the forwarder`).toBe(script)
      expect(
        words.slice(2),
        `${member} lists both programs`,
      ).toEqual(['tsconfig.json', 'tsconfig.browser.json'])
    }

    expect(withBrowser.length, 'members with a browser program').toBeGreaterThan(0)
  })
})
