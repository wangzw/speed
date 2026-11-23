/**
 * Focus hand-off after a write removed the control that started it.
 *
 * When a row write lands and its re-read drops the row, the control that
 * held focus leaves the document and the browser falls back to the body.
 * The page gives focus to the same control on the next row, else the
 * previous row's, else a stable landmark of the section (a heading the
 * caller made programmatically focusable).
 *
 * Only focus the removed control itself left behind is recovered. The
 * record of that comes from `useRowRemovalFocus`, which reads the active
 * element from the control's ref callback the moment React detaches it --
 * before the node leaves the document. A reader who clicked into the modal
 * paper, another control or anywhere else while the write ran has chosen a
 * new resting place, and that place is left alone even though the paper
 * carries the dialog role of the panel that is still open.
 *
 * The row control carries the value as an attribute (`data-role-remove`,
 * `data-grant-revoke`, ...). While the row is still listed, its position
 * names the next or previous control; once the re-read dropped it, the
 * recorded index names the row that moved up.
 */

import { useCallback, useRef } from 'react'
import type { RefCallback } from 'react'

/** The focus record of the row controls a page may remove. */
export interface RowRemovalFocus {
  /**
   * The ref callback for the row control carrying `value`. One callback is
   * kept per value, so React never re-attaches a control it did not move
   * and the mount/unmount pair stays matched.
   */
  readonly controlRef: (value: string) => RefCallback<HTMLElement>
  /**
   * Whether the control carrying `value` held focus when it left the
   * document; the record is cleared as it is read.
   */
  readonly takeHeldFocus: (value: string) => boolean
}

/** What one row control recorded about its last mount. */
interface RemovalRecord {
  node: HTMLElement | null
  held: boolean
}

/**
 * Tracks which row control held focus when it left the document.
 *
 * React calls a ref callback with null before it detaches the node, so the
 * active element still names the control there. A later landing asks
 * `takeHeldFocus` for the value it recorded, and only a control that held
 * focus hands it on.
 */
export function useRowRemovalFocus(): RowRemovalFocus {
  const records = useRef(new Map<string, RemovalRecord>())
  const callbacks = useRef(new Map<string, RefCallback<HTMLElement>>())

  const controlRef = useCallback((value: string) => {
    const cached = callbacks.current.get(value)
    if (cached !== undefined) {
      return cached
    }
    let record = records.current.get(value)
    if (record === undefined) {
      record = { node: null, held: false }
      records.current.set(value, record)
    }
    const owned = record
    const callback: RefCallback<HTMLElement> = (node) => {
      if (node !== null) {
        // A remount starts a new record; a held flag from an earlier mount
        // belongs to that mount's removal and is not carried over.
        owned.node = node
        owned.held = false
        return
      }
      if (owned.node !== null && owned.node === document.activeElement) {
        owned.held = true
      }
      owned.node = null
    }
    callbacks.current.set(value, callback)
    return callback
  }, [])

  const takeHeldFocus = useCallback((value: string) => {
    const record = records.current.get(value)
    if (record === undefined) {
      return false
    }
    const held = record.held
    record.held = false
    return held
  }, [])

  return { controlRef, takeHeldFocus }
}

export function focusAfterRowRemoval(
  attribute: string,
  value: string,
  index: number,
  heldFocus: boolean,
  fallback: () => void,
): void {
  if (!heldFocus) {
    return
  }
  const controls = Array.from(
    document.querySelectorAll<HTMLElement>(`[${attribute}]`),
  )
  const listed = controls.findIndex(
    (control) => control.getAttribute(attribute) === value,
  )
  const target =
    listed >= 0
      ? (controls[listed + 1] ?? controls[listed - 1])
      : (controls[index] ?? controls[index - 1])
  if (target !== undefined) {
    target.focus()
    return
  }
  fallback()
}
