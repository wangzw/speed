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
 * The per-spec directories a failed test keeps -- the e2e server's
 * temporary directory and the deploy fixture's volume -- accumulate without
 * bound in the same way, so they are swept by the same age rule. They live
 * under the system temp directory, which is not `/tmp` on every platform, and
 * each family is matched by its own whole name rather than by the shared
 * `speed-e2e-` prefix.
 *
 * Nothing here may remove anything else. Two guards hold the run-directory
 * rule: the scan only looks at direct children of the prefix root whose
 * *whole* name matches the shape `paths.ts` mints, and `removeRunDirectory`
 * refuses a path that is not such a child of the root it is given. The
 * per-spec sweep reads direct children of the system temp root and matches
 * only the two directory shapes the fixtures mint. A symlink is never
 * followed: a dirent that is not a real directory is skipped, and an explicit
 * removal of a symlink is refused, so a link planted under work the harness's
 * own name cannot redirect a deletion outside the root.
 *
 * A directory being deleted may hold files whose permissions deny writing.
 * `rm` cannot unlink a child of a read-only directory, so the deletion
 * restores the owner's write and search bits first, walking the tree without
 * following a symbolic link.
 *
 * The roots are parameters so the rules can be exercised against a temporary
 * directory; the harness leaves the run root at `/tmp`, the root
 * `ARTIFACT_ROOT` is composed under, and the per-spec root at the system temp
 * directory the fixtures create in.
 */
import { chmod, lstat, readdir, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { basename, dirname, join, resolve } from 'node:path'

/** The root the harness's per-run directories live under. */
export const RETENTION_ROOT = '/tmp'

/** The system temp root the per-spec server and deploy directories live under. */
export const PER_SPEC_ROOT = tmpdir()

/**
 * The shape of a run directory name: `speed-e2e-<pid>-<base36 clock>-<hex>`.
 * It is deliberately the whole name, not a prefix test: the same tree also
 * holds the per-spec directories `speed-e2e-server-*` and
 * `speed-e2e-deploy-*`, which share the string prefix.
 */
const RUN_DIRECTORY_NAME = /^speed-e2e-\d+-[0-9a-z]+-[0-9a-f]{8}$/

/** The shape of a per-spec e2e server directory: `speed-e2e-server-<six>`. */
const SERVER_DIRECTORY_NAME = /^speed-e2e-server-[A-Za-z0-9]{6}$/

/** The shape of a deploy fixture's kept volume: `speed-e2e-deploy-<six>`. */
const DEPLOY_DIRECTORY_NAME = /^speed-e2e-deploy-[A-Za-z0-9]{6}$/

/** How long a kept run or per-spec directory is retained before a sweep. */
export const RETENTION_WINDOW_MS = 24 * 60 * 60 * 1000

/** Where the rule looks and what "now" is, for the tests' temporary root. */
export interface RetentionOptions {
  /** The directory whose direct children are run directories. */
  readonly prefixRoot?: string
  /** The clock the age test reads, in milliseconds since the epoch. */
  readonly now?: number
}

/** Where the per-spec sweep looks and what "now" is. */
export interface PerSpecRetentionOptions {
  /** The directory whose direct children are per-spec directories. */
  readonly root?: string
  /** The clock the age test reads, in milliseconds since the epoch. */
  readonly now?: number
}

/** Whether a name is exactly one of the harness's run directory names. */
function isRunDirectoryName(name: string): boolean {
  return RUN_DIRECTORY_NAME.test(name)
}

/** Whether a name is exactly a per-spec server or deploy directory. */
function isPerSpecDirectoryName(name: string): boolean {
  return SERVER_DIRECTORY_NAME.test(name) || DEPLOY_DIRECTORY_NAME.test(name)
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
 * Delete `path` and everything under it, restoring the owner's write
 * permission first. Unlinking a child needs write permission on its directory,
 * so a directory whose owner bits deny writing gets them back before the walk
 * lists it; a file gets its owner's write bit back as well. A symbolic link is
 * unlinked, never followed: its target's permissions are left alone.
 */
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
    // Search (x) lets the walk list the directory; write (w) lets the final
    // unlink of each child succeed.
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

/**
 * Remove every direct child of `root` whose name `matches` and whose
 * modification time is older than the retention window. Answers the paths it
 * removed, so the caller can say what happened; a directory at or inside the
 * window is left alone, as is anything whose name does not match.
 *
 * Only direct children are considered, and only real directories: a symbolic
 * link is skipped rather than followed.
 */
async function removeExpiredMatching(
  root: string,
  matches: (name: string) => boolean,
  now: number,
): Promise<readonly string[]> {
  let entries
  try {
    entries = await readdir(root, { withFileTypes: true })
  } catch (error) {
    // A missing root holds nothing. Any other failure is real and must not be
    // read as "nothing to do".
    if (isNotFound(error)) return []
    throw error
  }

  const removed: string[] = []
  for (const entry of entries) {
    if (!entry.isDirectory()) continue
    if (!matches(entry.name)) continue
    const path = join(root, entry.name)

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

    await removeTree(path)
    removed.push(path)
  }
  return removed
}

/**
 * Remove every run directory under `options.prefixRoot` whose modification
 * time is older than the retention window. Answers the paths it removed, so
 * the caller can say what happened; a run directory at or inside the window is
 * left alone, as is anything whose name is not the harness's own shape.
 *
 * Only direct children are considered, and only real directories: a symbolic
 * link is skipped rather than followed.
 */
export async function removeExpiredRunDirectories(
  options: RetentionOptions = {},
): Promise<readonly string[]> {
  return removeExpiredMatching(
    options.prefixRoot ?? RETENTION_ROOT,
    isRunDirectoryName,
    options.now ?? Date.now(),
  )
}

/**
 * Remove the per-spec directories a failed test keeps -- the e2e server's
 * temporary directory and the deploy fixture's volume -- once their
 * modification time is older than the retention window. They live under the
 * system temp directory, which is not `/tmp` on every platform, and each is
 * matched by its own whole name. A failed run's directory is fresh and is
 * kept; only direct children that are real directories are considered, and a
 * symbolic link is never followed.
 */
export async function removeExpiredPerSpecDirectories(
  options: PerSpecRetentionOptions = {},
): Promise<readonly string[]> {
  return removeExpiredMatching(
    options.root ?? PER_SPEC_ROOT,
    isPerSpecDirectoryName,
    options.now ?? Date.now(),
  )
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

  await removeTree(candidate)
  return true
}
