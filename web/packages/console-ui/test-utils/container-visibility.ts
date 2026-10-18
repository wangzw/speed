/**
 * Reads a table column's responsive visibility out of the CSS emotion
 * injected for the current render. jsdom evaluates neither real layout
 * nor container queries, so the proof is the declarations the cell
 * carries: `display: none` at the container's 0px baseline and
 * `display: table-cell` from a named minimum width up. The width is the
 * table container's own minimum (DataTable's root box declares
 * `container-type: inline-size`; every priority rule resolves against
 * it), never the viewport's -- a 1184px container therefore shows a
 * column with a 600px or 900px restore and hides one restored at 1200px.
 */

import { emittedStyleText } from '@speed/test-utils/emitted-css'

/**
 * The declarations each `@container (min-width: Npx)` rule gives
 * `element`, keyed by N. Container-keyed styling is pure CSS emitted into
 * the stylesheet, and jsdom evaluates no container query, so the declared
 * value can only be read from the generated rules -- matched against the
 * element so a sibling cell's rule is not mistaken for this one. The
 * baseline (`@`) rule appears here as the key 0, which is how a cap that
 * applies below `md` is read.
 */
export function containerDeclarationsOf(
  element: Element,
): Map<number, string> {
  const css = emittedStyleText()
  const declarations = new Map<number, string>()
  const atRule = /@container \(min-width:(\d+)px\)\{/g
  let match: RegExpExecArray | null
  while ((match = atRule.exec(css)) !== null) {
    const open = atRule.lastIndex - 1
    let depth = 0
    let end = open
    for (; end < css.length; end += 1) {
      if (css[end] === '{') depth += 1
      else if (css[end] === '}') {
        depth -= 1
        if (depth === 0) break
      }
    }
    const body = css.slice(open + 1, end)
    const rule = /([^{}]+)\{([^{}]*)\}/g
    let inner: RegExpExecArray | null
    while ((inner = rule.exec(body)) !== null) {
      const selector = inner[1]?.trim() ?? ''
      if (!selector.includes('.css-')) continue
      try {
        if (!element.matches(selector)) continue
      } catch {
        continue
      }
      declarations.set(Number(match[1]), inner[2] ?? '')
    }
  }
  return declarations
}

/**
 * The max-width an element declares, keyed by the container width the
 * declaration applies from. The shared primary-column cap therefore reads
 * as 160 at the 0px baseline and 200 from 900px (`md`) up.
 */
export function containerMaxWidthOf(element: Element): Map<number, number> {
  const widths = new Map<number, number>()
  for (const [container, declarations] of containerDeclarationsOf(element)) {
    const cap = /max-width:(\d+)px/.exec(declarations)
    if (cap?.[1] !== undefined) widths.set(container, Number(cap[1]))
  }
  return widths
}

export interface ColumnVisibility {
  /** The container width the column is hidden at (the 0px baseline). */
  readonly hiddenAt: number
  /** The container width from which the column is shown again. */
  readonly visibleFrom: number
}

function escape(className: string): string {
  return className.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

/**
 * The visibility pair a priority column carries, or null for a column
 * with no priority (no visibility rule at all, so it never hides).
 */
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
