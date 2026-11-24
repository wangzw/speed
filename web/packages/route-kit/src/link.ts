/**
 * The click judgement behind every in-app link: intercept only a plain primary
 * click. `@speed/layout-kit` implements the same policy independently with
 * different edges (button 0 required, only `_self`, `currentTarget`); change both.
 */

import type { MouseEvent, MouseEventHandler } from 'react'

export function isPlainPrimaryClick(event: MouseEvent<HTMLElement>): boolean {
  if (event.button !== 0) return false
  if (event.metaKey || event.altKey || event.ctrlKey || event.shiftKey) {
    return false
  }
  if (event.defaultPrevented) return false
  const target = event.currentTarget.getAttribute('target')
  if (target !== null && target !== '' && target !== '_self') return false
  if (event.currentTarget.hasAttribute('download')) return false
  return true
}

export function createLinkClickHandler(
  to: string,
  navigate: (to: string) => void,
): MouseEventHandler<HTMLElement> {
  return (event) => {
    if (!isPlainPrimaryClick(event)) return
    event.preventDefault()
    navigate(to)
  }
}
