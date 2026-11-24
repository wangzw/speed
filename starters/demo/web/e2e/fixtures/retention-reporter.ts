import type { FullResult, Reporter } from '@playwright/test/reporter'

import { ARTIFACT_ROOT } from './paths'
import { RETENTION_ROOT, removeRunDirectory } from './retention'

export interface RunRetentionReporterOptions {
  readonly artifactRoot?: string
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
