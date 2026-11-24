/**
 * export-file: the audit export's file name and browser handoff.
 *
 * The name is the export's own instant written in UTC in the compact
 * layout the route's Content-Disposition carries, so a file saved here
 * matches one fetched directly from the route. The handoff turns the
 * Blob into a download through an object URL, then revokes the URL after
 * the browser has had a turn to start the download.
 */

import { afterEach, describe, expect, it, vi } from 'vitest'
import { restoreRealTimers } from '@speed/test-utils/timers'
import { exportFileName, saveBlob } from './export-file.js'
import { captureDownloads } from '../../test-utils/download.js'

describe('exportFileName', () => {
  it('writes the export instant in compact UTC', () => {
    expect(exportFileName(new Date('2026-10-24T10:15:00Z'))).toBe(
      'audit-records-20261024T101500Z.ndjson',
    )
  })

  it('zero-pads every single-digit component', () => {
    expect(exportFileName(new Date('2026-01-02T03:04:05Z'))).toBe(
      'audit-records-20260102T030405Z.ndjson',
    )
  })

  it('carries the instant in UTC rather than the process-local zone', () => {
    // The same instant reached through an explicit offset: the name is
    // its UTC reading, not the offset's wall clock.
    expect(exportFileName(new Date('2026-03-01T01:30:00+02:00'))).toBe(
      'audit-records-20260228T233000Z.ndjson',
    )
  })
})

describe('saveBlob', () => {
  let capture: ReturnType<typeof captureDownloads> | undefined

  afterEach(async () => {
    capture?.restore()
    capture = undefined
    await restoreRealTimers()
  })

  it('downloads the bytes under the given name and revokes the URL after a turn', () => {
    vi.useFakeTimers()
    capture = captureDownloads()
    const blob = new Blob(['{"id":"rec-1"}\n'], {
      type: 'application/x-ndjson',
    })

    saveBlob(blob, 'audit-records-20261024T101500Z.ndjson')

    expect(capture.blobs()).toEqual([blob])
    const downloads = capture.downloads()
    expect(downloads).toHaveLength(1)
    expect(downloads[0]!.fileName).toBe(
      'audit-records-20261024T101500Z.ndjson',
    )
    expect(downloads[0]!.href).toBe('blob:captured-1')
    // The anchor is temporary: it must not stay in the document once the
    // click is dispatched.
    expect(document.querySelector('a')).toBeNull()
    // Revoking while the download is starting would cancel it, so the URL
    // outlives the call and is released on a later tick.
    expect(capture.revoked()).toEqual([])
    vi.runAllTimers()
    expect(capture.revoked()).toEqual(['blob:captured-1'])
  })
})
