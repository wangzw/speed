/**
 * The retention rule for the harness's per-run directories.
 *
 * Every run keeps its build, report, screenshots and server logs under
 * `/tmp/speed-e2e-<run-id>`. A failed run's directory is evidence and is kept;
 * a successful one has served its purpose but is not deleted while the report
 * is still being written -- the reporter calls `removeRunDirectory` from
 * `onExit`, after every reporter's `onEnd`, so the HTML report is complete when
 * the directory goes.
 *
 * Nothing here may remove anything else under /tmp. Two guards hold that: the
 * scan only looks at direct children of the prefix root whose *whole* name
 * matches the shape `paths.ts` mints, and `removeRunDirectory` refuses a path
 * that is not such a child of the root it is given. A symlink is never
 * followed: a dirent that is not a real directory is skipped, and an explicit
 * removal of a symlink is refused, so a link planted under work the harness's
 * own name cannot redirect a deletion outside `/tmp`.
 *
 * The prefix root is a parameter so the rule can be exercised against a
 * temporary directory; the harness leaves it at `/tmp`, the root
 * `ARTIFACT_ROOT` is composed under.
 */
import { lstat, readdir, rm } from 'node:fs/promises'
import { basename, dirname, join, resolve } from 'node:path'

/** The root the harness's per-run directories live under. */
export const RETENTION_ROOT = '/tmp'

/**
 * The shape of a run directory name: `speed-e2e-<pid>-<base36 clock>-<hex>`.
 * It is deliberately the whole name, not a prefix test: `/tmp` also holds the
 * per-spec server directories `speed-e2e-server-*`, which share the string
 * prefix and must never be swept.
 */
const RUN_DIRECTORY_NAME = /^speed-e2e-\d+-[0-9a-z]+-[0-9a-f]{8}$/

/** How long a run directory is kept before the next run may remove it. */
export const RETENTION_WINDOW_MS = 7 * 24 * 60 * 60 * 1000

/** Where the rule looks and what "now" is, for the tests' temporary root. */
export interface RetentionOptions {
  /** The directory whose direct children are run directories. */
  readonly prefixRoot?: string
  /** The clock the age test reads, in milliseconds since the epoch. */
  readonly now?: number
}

/** Whether a name is exactly one of the harness's run directory names. */
function isRunDirectoryName(name: string): boolean {
  return RUN_DIRECTORY_NAME.test(name)
}

/** Whether an error is the filesystem saying the path is not there. */
function isNotFound(error: unknown): boolean {
  return (
    typeof error === 'object' &&
    error !== null &&
    (error as { code?: unknown }).code === 'ENOENT'
  )
}

/**
 * Remove every run directory under the prefix root whose modification time is
 * older than the retention window. Answers the paths it removed, so the caller
 * can say what happened; a run directory at or inside the window is left
 * alone, as is anything whose name is not the harness's own shape.
 *
 * Only direct children are considered, and only real directories: a symbolic
 * link is skipped rather than followed.
 */
export async function removeExpiredRunDirectories(
  options: RetentionOptions = {},
): Promise<readonly string[]> {
  const prefixRoot = options.prefixRoot ?? RETENTION_ROOT
  const now = options.now ?? Date.now()

  let entries
  try {
    entries = await readdir(prefixRoot, { withFileTypes: true })
  } catch (error) {
    // A missing root holds no run directories. Any other failure is real and
    // must not be read as "nothing to do".
    if (isNotFound(error)) return []
    throw error
  }

  const removed: string[] = []
  for (const entry of entries) {
    if (!entry.isDirectory()) continue
    if (!isRunDirectoryName(entry.name)) continue
    const path = join(prefixRoot, entry.name)

    // The dirent already says directory (not symlink), but the filesystem may
    // have changed between the listing and this call; decide on a fresh
    // lstat, which never follows a link.
    let info
    try {
      info = await lstat(path)
    } catch (error) {
      if (isNotFound(error)) continue
      throw error
    }
    if (!info.isDirectory()) continue
    if (now - info.mtimeMs <= RETENTION_WINDOW_MS) continue

    await rm(path, { recursive: true, force: true })
    removed.push(path)
  }
  return removed
}

/**
 * Remove one run directory, but only this run's own: the path must be a direct
 * child of the prefix root and its whole name must be the shape the harness
 * mints. A path that is not a directory -- including a symlink -- is refused
 * rather than followed or unlinked, so a planted link cannot take a deletion
 * out of the prefix.
 *
 * Answers whether the directory was there to remove; an absent path is already
 * in the state a successful run leaves it in.
 */
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

  await rm(candidate, { recursive: true, force: true })
  return true
}
