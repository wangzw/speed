/** The audit export's file name and browser handoff. */

const EXPORT_FILE_PREFIX = 'audit-records-'
const EXPORT_FILE_SUFFIX = '.ndjson'

/** The compact UTC instant the export file name carries, as the route names it. */
function compactInstant(at: Date): string {
  return at.toISOString().replaceAll('-', '').replaceAll(':', '').replace(/\.\d+Z$/, 'Z')
}

/** The download name for an export started at the given instant. */
export function exportFileName(at: Date): string {
  return `${EXPORT_FILE_PREFIX}${compactInstant(at)}${EXPORT_FILE_SUFFIX}`
}

/**
 * Saves the exported bytes as a file download under the given name. The
 * anchor is appended before clicking: a detached anchor's click does not
 * start a download everywhere.
 */
export function saveBlob(blob: Blob, fileName: string): void {
  const url = URL.createObjectURL(blob)
  const revoke = URL.revokeObjectURL
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = fileName
  anchor.rel = 'noopener'
  anchor.hidden = true
  document.body.appendChild(anchor)
  anchor.click()
  anchor.remove()
  // Revoke on a later tick: the browser starts the download asynchronously,
  // and revoking first can cancel it.
  setTimeout(() => {
    revoke(url)
  }, 0)
}
