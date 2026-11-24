/**
 * Reads a table column's responsive visibility from the injected emotion CSS:
 * `display: none` at the container's 0px baseline and `display: table-cell` from
 * a named minimum width up. The width is the table container's, never the viewport's.
 */

import {
  containerMaxWidthOf,
  emittedStyleText,
} from '@speed/test-utils/emitted-css'

export { containerMaxWidthOf }

export interface ColumnVisibility {
  /** The container width the column is hidden at (the 0px baseline). */
  readonly hiddenAt: number
  /** The container width from which the column is shown again. */
  readonly visibleFrom: number
}

function escape(className: string): string {
  return className.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

/** The visibility pair a priority column carries, or null for an unprioritized column. */
export function columnVisibility(element: Element): ColumnVisibility | null {
  const css = emittedStyleText()
  const classes = Array.from(element.classList).filter((name) =>
    name.startsWith('css-'),
  )
  for (const className of classes) {
    const name = escape(className)
    const hidden = new RegExp(
      `@container \\(min-width:(\\d+)px\\)\\{\\.${name}\\{[^}]*display:none`,
    ).exec(css)
    const shown = new RegExp(
      `@container \\(min-width:(\\d+)px\\)\\{\\.${name}\\{[^}]*display:table-cell`,
    ).exec(css)
    if (hidden !== null && shown !== null) {
      return {
        hiddenAt: Number(hidden[1]),
        visibleFrom: Number(shown[1]),
      }
    }
  }
  return null
}
