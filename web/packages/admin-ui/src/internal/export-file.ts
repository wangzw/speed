/**
 * export-file.ts: the audit export's file name and browser handoff.
 *
 * The name is the export's own instant written in UTC in the compact
 * layout the route's Content-Disposition carries (audit-records-<instant>.ndjson),
 * so a file saved here is named like one fetched directly from the route.
 * The handoff turns the Blob into a download through an object URL and a
 * temporary anchor, then revokes the URL after the browser has had a turn
 * to start the download: revoking while it starts can cancel it.
 */

const EXPORT_FILE_PREFIX = 'audit-records-'
const EXPORT_FILE_SUFFIX = '.ndjson'

/**
 * The compact UTC instant the export file name carries: the ISO 8601
 * instant without its separators or fractional seconds, e.g.
 * 20261024T101500Z.
 */
function compactInstant(at: Date): string {
  return at.toISOString().replaceAll('-', '').replaceAll(':', '').replace(/\.\d+Z$/, 'Z')
}

/** The download name for an export started at the given instant. */
export function exportFileName(at: Date): string {
  return `${EXPORT_FILE_PREFIX}${compactInstant(at)}${EXPORT_FILE_SUFFIX}`
}

/**
 * Saves the exported bytes as a file download under the given name. The
 * anchor is temporary and never rendered: it is appended (a detached
 * anchor's click does not start a download everywhere), clicked, then
 * removed.
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
  // A later tick, not this one: the browser starts the download
  // asynchronously, and revoking the URL first can cancel it.
  setTimeout(() => {
    revoke(url)
  }, 0)
}
