import { chmod, mkdir, mkdtemp, rm, stat, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

import type { FullResult } from '@playwright/test/reporter'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import RunRetentionReporter from './retention-reporter'

const RUN_DIRECTORY = 'speed-e2e-4242-m1abc2de-0a1b2c3d'

let prefixRoot: string
let runDirectory: string

function resultWith(status: FullResult['status']): FullResult {
  return { status } as FullResult
}

async function exists(path: string): Promise<boolean> {
  try {
    await stat(path)
    return true
  } catch {
    return false
  }
}

function reporterFor(root: string): RunRetentionReporter {
  return new RunRetentionReporter({
    artifactRoot: runDirectory,
    retentionRoot: root,
  })
}

beforeEach(async () => {
  prefixRoot = await mkdtemp(join(tmpdir(), 'speed-e2e-reporter-test-'))
  runDirectory = join(prefixRoot, RUN_DIRECTORY)
  await mkdir(runDirectory)
})

afterEach(async () => {
  await rm(prefixRoot, { recursive: true, force: true })
})

describe('RunRetentionReporter', () => {
  it("removes this run's directory once a passed run has written its report", async () => {
    const reporter = reporterFor(prefixRoot)

    reporter.onEnd(resultWith('passed'))
    await reporter.onExit()

    await expect(exists(runDirectory)).resolves.toBe(false)
  })

  it('keeps the directory of a failed run for diagnosis', async () => {
    const reporter = reporterFor(prefixRoot)

    reporter.onEnd(resultWith('failed'))
    await reporter.onExit()

    await expect(exists(runDirectory)).resolves.toBe(true)
  })

  it('keeps the directory of a timed-out run', async () => {
    const reporter = reporterFor(prefixRoot)

    reporter.onEnd(resultWith('timedout'))
    await reporter.onExit()

    await expect(exists(runDirectory)).resolves.toBe(true)
  })

  it('keeps the directory of an interrupted run', async () => {
    const reporter = reporterFor(prefixRoot)

    reporter.onEnd(resultWith('interrupted'))
    await reporter.onExit()

    await expect(exists(runDirectory)).resolves.toBe(true)
  })

  it('keeps the directory when onExit runs without a preceding onEnd', async () => {
    const reporter = reporterFor(prefixRoot)

    await reporter.onExit()

    await expect(exists(runDirectory)).resolves.toBe(true)
  })

  it('restores write permission before removing a passed run with read-only files', async () => {
    const nested = join(runDirectory, 'nested')
    await mkdir(nested)
    const file = join(nested, 'kept.txt')
    await writeFile(file, 'evidence\n')
    await chmod(nested, 0o500)
    await chmod(file, 0o400)

    const reporter = reporterFor(prefixRoot)
    reporter.onEnd(resultWith('passed'))
    await reporter.onExit()

    await expect(exists(runDirectory)).resolves.toBe(false)
  })
})
