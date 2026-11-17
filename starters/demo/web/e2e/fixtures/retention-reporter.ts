/**
 * The run directory's fate, decided once the whole run is over.
 *
 * Playwright's global teardown receives no status, so it cannot tell a passed
 * run from a failed one. A reporter can: `onEnd` is called after global
 * teardown with the run's final result, and `onExit` after every reporter has
 * finished writing its report. `onExit` is where the deletion happens, so the
 * HTML report under the run directory is complete first and a run with several
 * specs is judged as a whole, not per test.
 *
 * A passed run removes its directory; anything else -- failed, timed out,
 * interrupted -- keeps it for diagnosis and names it in the run's output.
 */
import type { FullResult, Reporter } from '@playwright/test/reporter'

import { ARTIFACT_ROOT } from './paths'
import { RETENTION_ROOT, removeRunDirectory } from './retention'

/** Where the reporter deletes from, overridable by the tests. */
export interface RunRetentionReporterOptions {
  /** This run's own directory, the one a passed run removes. */
  readonly artifactRoot?: string
  /** The root a run directory must be a direct child of. */
  readonly retentionRoot?: string
}

class RunRetentionReporter implements Reporter {
  private readonly artifactRoot: string
  private readonly retentionRoot: string
  private status: FullResult['status'] | undefined

  constructor(options: RunRetentionReporterOptions = {}) {
    this.artifactRoot = options.artifactRoot ?? ARTIFACT_ROOT
    this.retentionRoot = options.retentionRoot ?? RETENTION_ROOT
  }

  onEnd(result: FullResult): void {
    this.status = result.status
  }

  async onExit(): Promise<void> {
    if (this.status !== 'passed') {
      process.stdout.write(
        `[e2e] run directory kept for diagnosis ` +
          `(run status: ${this.status ?? 'unknown'}): ${this.artifactRoot}\n` +
          `[e2e] HTML report: ${this.artifactRoot}/report/index.html\n`,
      )
      return
    }
    const removed = await removeRunDirectory(this.artifactRoot, {
      prefixRoot: this.retentionRoot,
    })
    if (removed) {
      process.stdout.write(`[e2e] run directory removed: ${this.artifactRoot}\n`)
    }
  }
}

export { RunRetentionReporter }
export default RunRetentionReporter
