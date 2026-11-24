import {
  chmod,
  lstat,
  lutimes,
  mkdir,
  mkdtemp,
  rm,
  stat,
  symlink,
  utimes,
  writeFile,
} from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import {
  RETENTION_WINDOW_MS,
  removeExpiredPerSpecDirectories,
  removeExpiredRunDirectories,
  removeRunDirectory,
} from './retention'

const RUN_DIRECTORY = 'speed-e2e-4242-m1abc2de-0a1b2c3d'

const PER_SPEC_SERVER = 'speed-e2e-server-a1B2c3'

const PER_SPEC_DEPLOY = 'speed-e2e-deploy-d4E5f6'

const SERVER_DIRECTORY = 'speed-e2e-server-ab12cd34'

const DAY_MS = 24 * 60 * 60 * 1000

let prefixRoot: string

async function makeDirectory(name: string): Promise<string> {
  const path = join(prefixRoot, name)
  await mkdir(path, { recursive: true })
  return path
}

async function backdate(path: string, now: number, ageMs: number): Promise<void> {
  const when = new Date(now - ageMs)
  await utimes(path, when, when)
}

async function exists(path: string): Promise<boolean> {
  try {
    await stat(path)
    return true
  } catch {
    return false
  }
}

beforeEach(async () => {
  prefixRoot = await mkdtemp(join(tmpdir(), 'speed-e2e-retention-test-'))
})

afterEach(async () => {
  await rm(prefixRoot, { recursive: true, force: true })
})

describe('removeExpiredRunDirectories', () => {
  it('removes an expired run directory under the prefix root', async () => {
    const now = Date.now()
    const run = await makeDirectory(RUN_DIRECTORY)
    await backdate(run, now, RETENTION_WINDOW_MS + DAY_MS)

    const removed = await removeExpiredRunDirectories({ prefixRoot, now })

    expect(removed).toEqual([run])
    await expect(exists(run)).resolves.toBe(false)
  })

  it('removes a run directory more than a day old', async () => {
    const now = Date.now()
    const run = await makeDirectory(RUN_DIRECTORY)
    await backdate(run, now, DAY_MS + 1_000)

    const removed = await removeExpiredRunDirectories({ prefixRoot, now })

    expect(removed).toEqual([run])
    await expect(exists(run)).resolves.toBe(false)
  })

  it('keeps a fresh run directory', async () => {
    const now = Date.now()
    const run = await makeDirectory(RUN_DIRECTORY)

    const removed = await removeExpiredRunDirectories({ prefixRoot, now })

    expect(removed).toEqual([])
    await expect(lstat(run)).resolves.toBeTruthy()
  })

  it('keeps a 23-hour-old run directory, inside the one-day window', async () => {
    const now = Date.now()
    const run = await makeDirectory(RUN_DIRECTORY)
    await backdate(run, now, 23 * 60 * 60 * 1000)

    const removed = await removeExpiredRunDirectories({ prefixRoot, now })

    expect(removed).toEqual([])
    await expect(exists(run)).resolves.toBe(true)
  })

  it('keeps a run directory just inside the one-day window and removes one just outside', async () => {
    const now = Date.now()
    const justInside = await makeDirectory('speed-e2e-4242-m1abc2de-00000001')
    const justOutside = await makeDirectory('speed-e2e-4242-m1abc2de-00000002')
    await backdate(justInside, now, RETENTION_WINDOW_MS - 1_000)
    await backdate(justOutside, now, RETENTION_WINDOW_MS + 1_000)

    const removed = await removeExpiredRunDirectories({ prefixRoot, now })

    expect(removed).toEqual([justOutside])
    await expect(exists(justInside)).resolves.toBe(true)
    await expect(exists(justOutside)).resolves.toBe(false)
  })

  it('never touches a similar name outside the prefix root', async () => {
    const outsideRoot = await mkdtemp(join(tmpdir(), 'speed-e2e-retention-outside-'))
    try {
      const now = Date.now()
      const outsideRun = join(outsideRoot, RUN_DIRECTORY)
      await mkdir(outsideRun)
      await backdate(outsideRun, now, RETENTION_WINDOW_MS + DAY_MS)

      const removed = await removeExpiredRunDirectories({ prefixRoot, now })

      expect(removed).toEqual([])
      await expect(lstat(outsideRun)).resolves.toBeTruthy()
      await expect(removeRunDirectory(outsideRun, { prefixRoot })).rejects.toThrow(
        /not a run directory directly under/,
      )
      await expect(lstat(outsideRun)).resolves.toBeTruthy()
    } finally {
      await rm(outsideRoot, { recursive: true, force: true })
    }
  })

  it('refuses a prefix-sharing path that does not match the run directory shape', async () => {
    const now = Date.now()
    const server = await makeDirectory(SERVER_DIRECTORY)
    const other = await makeDirectory('not-a-run-directory')
    await backdate(server, now, RETENTION_WINDOW_MS + DAY_MS)
    await backdate(other, now, RETENTION_WINDOW_MS + DAY_MS)

    const removed = await removeExpiredRunDirectories({ prefixRoot, now })

    expect(removed).toEqual([])
    await expect(lstat(server)).resolves.toBeTruthy()
    await expect(lstat(other)).resolves.toBeTruthy()
    await expect(removeRunDirectory(server, { prefixRoot })).rejects.toThrow(
      /not a run directory directly under/,
    )
    await expect(lstat(server)).resolves.toBeTruthy()
  })

  it('does not follow a symlink named like a run directory out of the prefix', async () => {
    const targetRoot = await mkdtemp(join(tmpdir(), 'speed-e2e-retention-target-'))
    try {
      const now = Date.now()
      const target = join(targetRoot, 'kept-outside')
      await mkdir(target)
      const link = join(prefixRoot, RUN_DIRECTORY)
      await symlink(target, link)
      const old = new Date(now - RETENTION_WINDOW_MS - DAY_MS)
      await lutimes(link, old, old)

      const removed = await removeExpiredRunDirectories({ prefixRoot, now })

      expect(removed).toEqual([])
      await expect(lstat(target)).resolves.toBeTruthy()
      await expect(lstat(link)).resolves.toBeTruthy()
      await expect(removeRunDirectory(link, { prefixRoot })).rejects.toThrow(
        /not a directory/,
      )
      await expect(lstat(target)).resolves.toBeTruthy()
    } finally {
      await rm(targetRoot, { recursive: true, force: true })
    }
  })
})

describe('removeExpiredPerSpecDirectories', () => {
  it('removes an expired server directory and an expired deploy volume', async () => {
    const now = Date.now()
    const server = await makeDirectory(PER_SPEC_SERVER)
    const deploy = await makeDirectory(PER_SPEC_DEPLOY)
    await backdate(server, now, RETENTION_WINDOW_MS + DAY_MS)
    await backdate(deploy, now, RETENTION_WINDOW_MS + DAY_MS)

    const removed = await removeExpiredPerSpecDirectories({
      root: prefixRoot,
      now,
    })

    expect([...removed].sort()).toEqual([server, deploy].sort())
    await expect(exists(server)).resolves.toBe(false)
    await expect(exists(deploy)).resolves.toBe(false)
  })

  it('keeps a per-spec directory inside the window', async () => {
    const now = Date.now()
    const server = await makeDirectory(PER_SPEC_SERVER)

    const removed = await removeExpiredPerSpecDirectories({
      root: prefixRoot,
      now,
    })

    expect(removed).toEqual([])
    await expect(lstat(server)).resolves.toBeTruthy()
  })

  it('keeps a prefix-sharing name that is not a per-spec shape', async () => {
    const now = Date.now()
    const nearMiss = await makeDirectory(SERVER_DIRECTORY)
    await backdate(nearMiss, now, RETENTION_WINDOW_MS + DAY_MS)

    const removed = await removeExpiredPerSpecDirectories({
      root: prefixRoot,
      now,
    })

    expect(removed).toEqual([])
    await expect(lstat(nearMiss)).resolves.toBeTruthy()
  })
})

describe('removeRunDirectory', () => {
  it("removes this run's own directory after a successful run", async () => {
    const run = await makeDirectory(RUN_DIRECTORY)

    await expect(removeRunDirectory(run, { prefixRoot })).resolves.toBe(true)

    await expect(exists(run)).resolves.toBe(false)
  })

  it('restores write permission before removing a read-only tree', async () => {
    const run = await makeDirectory(RUN_DIRECTORY)
    const nested = join(run, 'nested')
    await mkdir(nested)
    const file = join(nested, 'kept.txt')
    await writeFile(file, 'evidence\n')
    await chmod(nested, 0o500)
    await chmod(file, 0o400)

    await expect(removeRunDirectory(run, { prefixRoot })).resolves.toBe(true)

    await expect(exists(run)).resolves.toBe(false)
  })

  it('treats an absent run directory as already removed', async () => {
    await expect(
      removeRunDirectory(join(prefixRoot, RUN_DIRECTORY), { prefixRoot }),
    ).resolves.toBe(false)
  })
})
