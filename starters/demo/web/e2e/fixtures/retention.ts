import { chmod, lstat, readdir, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { basename, dirname, join, resolve } from 'node:path'

export const RETENTION_ROOT = '/tmp'

export const PER_SPEC_ROOT = tmpdir()

// The whole name, not a prefix test: the tree also holds `speed-e2e-server-*` and `speed-e2e-deploy-*`.
const RUN_DIRECTORY_NAME = /^speed-e2e-\d+-[0-9a-z]+-[0-9a-f]{8}$/

const SERVER_DIRECTORY_NAME = /^speed-e2e-server-[A-Za-z0-9]{6}$/

const DEPLOY_DIRECTORY_NAME = /^speed-e2e-deploy-[A-Za-z0-9]{6}$/

export const RETENTION_WINDOW_MS = 24 * 60 * 60 * 1000

export interface RetentionOptions {
  readonly prefixRoot?: string
  readonly now?: number
}

export interface PerSpecRetentionOptions {
  readonly root?: string
  readonly now?: number
}

function isRunDirectoryName(name: string): boolean {
  return RUN_DIRECTORY_NAME.test(name)
}

function isPerSpecDirectoryName(name: string): boolean {
  return SERVER_DIRECTORY_NAME.test(name) || DEPLOY_DIRECTORY_NAME.test(name)
}

function isNotFound(error: unknown): boolean {
  return (
    typeof error === 'object' &&
    error !== null &&
    (error as { code?: unknown }).code === 'ENOENT'
  )
}

// Unlinking a child needs write permission on its directory; a symlink is unlinked, never followed.
async function removeTree(path: string): Promise<void> {
  let info
  try {
    info = await lstat(path)
  } catch (error) {
    if (isNotFound(error)) return
    throw error
  }
  if (info.isSymbolicLink()) {
    await rm(path, { force: true })
    return
  }

  const permissions = info.mode & 0o7777
  if (info.isDirectory()) {
    if ((permissions & 0o700) !== 0o700) await chmod(path, permissions | 0o700)
    let entries
    try {
      entries = await readdir(path)
    } catch (error) {
      if (isNotFound(error)) return
      throw error
    }
    for (const entry of entries) await removeTree(join(path, entry))
  } else if ((permissions & 0o600) !== 0o600) {
    await chmod(path, permissions | 0o600)
  }

  await rm(path, { recursive: true, force: true })
}

async function removeExpiredMatching(
  root: string,
  matches: (name: string) => boolean,
  now: number,
): Promise<readonly string[]> {
  let entries
  try {
    entries = await readdir(root, { withFileTypes: true })
  } catch (error) {
    if (isNotFound(error)) return []
    throw error
  }

  const removed: string[] = []
  for (const entry of entries) {
    if (!entry.isDirectory()) continue
    if (!matches(entry.name)) continue
    const path = join(root, entry.name)

    // Decide on a fresh lstat: the listing may be stale, and lstat never follows a link.
    let info
    try {
      info = await lstat(path)
    } catch (error) {
      if (isNotFound(error)) continue
      throw error
    }
    if (!info.isDirectory()) continue
    if (now - info.mtimeMs <= RETENTION_WINDOW_MS) continue

    await removeTree(path)
    removed.push(path)
  }
  return removed
}

export async function removeExpiredRunDirectories(
  options: RetentionOptions = {},
): Promise<readonly string[]> {
  return removeExpiredMatching(
    options.prefixRoot ?? RETENTION_ROOT,
    isRunDirectoryName,
    options.now ?? Date.now(),
  )
}

export async function removeExpiredPerSpecDirectories(
  options: PerSpecRetentionOptions = {},
): Promise<readonly string[]> {
  return removeExpiredMatching(
    options.root ?? PER_SPEC_ROOT,
    isPerSpecDirectoryName,
    options.now ?? Date.now(),
  )
}

// A path that is not a real directory directly under the prefix root is refused, so a planted symlink cannot take a deletion out of it.
export async function removeRunDirectory(
  path: string,
  options: RetentionOptions = {},
): Promise<boolean> {
  const candidate = resolve(path)
  const prefixRoot = resolve(options.prefixRoot ?? RETENTION_ROOT)
  if (dirname(candidate) !== prefixRoot || !isRunDirectoryName(basename(candidate))) {
    throw new Error(
      `refusing to remove ${path}: not a run directory directly under ${prefixRoot}`,
    )
  }

  let info
  try {
    info = await lstat(candidate)
  } catch (error) {
    if (isNotFound(error)) return false
    throw error
  }
  if (!info.isDirectory()) {
    throw new Error(
      `refusing to remove ${candidate}: it is not a directory, and a symlink is never followed`,
    )
  }

  await removeTree(candidate)
  return true
}
