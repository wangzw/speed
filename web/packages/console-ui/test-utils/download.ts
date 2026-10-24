/**
 * download.ts -- the browser download plumbing stubbed for the suites.
 *
 * jsdom implements neither URL.createObjectURL/revokeObjectURL nor the
 * navigation an anchor click starting a download would perform, so a
 * suite that exercises a file handoff would fail on the missing API.
 * This helper replaces the two object-URL entry points and records the
 * anchor clicks the code under test dispatched, so a test can assert the
 * file name, the URL and the bytes without a browser.
 */

import { vi } from 'vitest'

/** One download the code under test started. */
export interface CapturedDownload {
  /** The anchor's `download` attribute -- the file name. */
  readonly fileName: string
  /** The URL the anchor pointed at. */
  readonly href: string
}

export interface DownloadCapture {
  /** The blobs the object URLs were created from, in call order. */
  blobs(): readonly Blob[]
  /** The downloads started, in call order. */
  downloads(): readonly CapturedDownload[]
  /** The object URLs revoked so far. */
  revoked(): readonly string[]
  /** Puts the global URL and the anchor's click back. */
  restore(): void
}

/**
 * Installs the capture for one test and returns what it observed. Call
 * restore() when the test ends; a revoke scheduled by the code under test
 * keeps pointing at the capture, so it stays safe after the globals are
 * back.
 */
export function captureDownloads(): DownloadCapture {
  const blobs: Blob[] = []
  const downloads: CapturedDownload[] = []
  const revoked: string[] = []

  const originalCreate: typeof URL.createObjectURL | undefined =
    URL.createObjectURL
  const originalRevoke: typeof URL.revokeObjectURL | undefined =
    URL.revokeObjectURL

  URL.createObjectURL = (blob: Blob | MediaSource): string => {
    blobs.push(blob as Blob)
    return `blob:captured-${blobs.length}`
  }
  URL.revokeObjectURL = (url: string): void => {
    revoked.push(url)
  }
  const click = vi
    .spyOn(HTMLAnchorElement.prototype, 'click')
    .mockImplementation(function (this: unknown): void {
      const anchor = this as HTMLAnchorElement
      downloads.push({
        fileName: anchor.download,
        href: anchor.getAttribute('href') ?? '',
      })
    })

  return {
    blobs: () => blobs,
    downloads: () => downloads,
    revoked: () => revoked,
    restore: () => {
      click.mockRestore()
      if (originalCreate === undefined) {
        Reflect.deleteProperty(URL, 'createObjectURL')
      } else {
        URL.createObjectURL = originalCreate
      }
      if (originalRevoke === undefined) {
        Reflect.deleteProperty(URL, 'revokeObjectURL')
      } else {
        URL.revokeObjectURL = originalRevoke
      }
    },
  }
}
