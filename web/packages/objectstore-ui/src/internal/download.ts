/**
 * Saving a fetched object's bytes to the user's disk.
 *
 * A download's bytes arrive as a Blob (the generated content operation
 * declares a raw-byte response, which the api-client resolves as a Blob).
 * The browser is handed the bytes through a temporary object URL on a
 * synthetic anchor with a `download` attribute; the anchor is clicked and
 * removed, and the URL is revoked immediately after. This is the one
 * place the page touches the DOM outside React, and it performs no
 * network work: the bytes are already in hand.
 *
 * The object URL API is feature-detected: an environment without
 * createObjectURL (a test DOM) still gets the anchor, whose empty href is
 * inert, so the function never throws on the code path a suite drives.
 */

/** Hands the given bytes to the browser as a download named `filename`. */
export function saveBlob(blob: Blob, filename: string): void {
  const canCreateUrl =
    typeof URL !== 'undefined' && typeof URL.createObjectURL === 'function'
  const url = canCreateUrl ? URL.createObjectURL(blob) : ''
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = filename
  anchor.rel = 'noopener'
  document.body.appendChild(anchor)
  anchor.click()
  document.body.removeChild(anchor)
  if (canCreateUrl) {
    URL.revokeObjectURL(url)
  }
}
