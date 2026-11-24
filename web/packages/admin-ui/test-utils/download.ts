/**
 * The browser download plumbing stubbed for the suites: jsdom implements neither
 * object URLs nor a downloading anchor click, so this replaces the object-URL
 * entry points and records the anchor clicks.
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
 * Installs the capture for one test and returns what it observed; call
 * `restore()` when the test ends.
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
