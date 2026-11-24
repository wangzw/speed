/**
 * Focus hand-off after a write removed the control: next row's same control, else
 * the previous row's, else a section landmark.
 */

import { useCallback, useRef } from 'react'
import type { RefCallback } from 'react'

/** The focus record of the row controls a page may remove. */
export interface RowRemovalFocus {
  /** The ref callback for the row control carrying `value`. */
  readonly controlRef: (value: string) => RefCallback<HTMLElement>
  /** Whether the control carrying `value` held focus when it left; cleared as it is read. */
  readonly takeHeldFocus: (value: string) => boolean
}

interface RemovalRecord {
  node: HTMLElement | null
  held: boolean
}

/**
 * Tracks which row control held focus when it left the document. React calls
 * a ref callback with null before detaching the node, so the active element
 * still names the control there.
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
        // A remount starts a new record: a held flag from an earlier mount
        // belongs to that mount's removal.
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
