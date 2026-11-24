export function emittedStyleText(): string {
  return Array.from(document.querySelectorAll('style'))
    .map((style) => style.textContent ?? '')
    .join('\n')
}

// A literal because the dependency runs the other way: the presentational
// packages depend on this helper, so it cannot import the ui-kit constant.
const COARSE_POINTER_MEDIA = '@media (pointer: coarse)'

function coarseMediaBodies(css: string): string[] {
  const bodies: string[] = []
  let cursor = 0
  for (;;) {
    const start = css.indexOf(COARSE_POINTER_MEDIA, cursor)
    if (start < 0) return bodies
    const open = css.indexOf('{', start)
    if (open < 0) return bodies
    let depth = 0
    let end = open
    for (; end < css.length; end += 1) {
      if (css[end] === '{') depth += 1
      else if (css[end] === '}') {
        depth -= 1
        if (depth === 0) break
      }
    }
    bodies.push(css.slice(open + 1, end))
    cursor = end + 1
  }
}

interface FlatRule {
  readonly selector: string
  readonly declarations: string
}

function flatRules(body: string): FlatRule[] {
  const rules: FlatRule[] = []
  let cursor = 0
  while (cursor < body.length) {
    const open = body.indexOf('{', cursor)
    if (open < 0) break
    const selector = body.slice(cursor, open).trim()
    let depth = 0
    let end = open
    for (; end < body.length; end += 1) {
      if (body[end] === '{') depth += 1
      else if (body[end] === '}') {
        depth -= 1
        if (depth === 0) break
      }
    }
    rules.push({ selector, declarations: body.slice(open + 1, end) })
    cursor = end + 1
  }
  return rules
}

function matchesSelector(element: Element, selector: string): boolean {
  try {
    return element.matches(selector)
  } catch {
    return false
  }
}

export function hasCoarsePointerTouchTarget(element: Element): boolean {
  for (const body of coarseMediaBodies(emittedStyleText())) {
    for (const rule of flatRules(body)) {
      if (
        !rule.declarations.includes('min-width:44px') ||
        !rule.declarations.includes('min-height:44px')
      ) {
        continue
      }
      if (matchesSelector(element, rule.selector)) return true
    }
  }
  return false
}

export interface TouchTargetSample {
  readonly category: string
  readonly element: Element | null
}

export function missingCoarsePointerTouchTargets(
  samples: readonly TouchTargetSample[],
): string[] {
  return samples
    .filter(
      (sample) =>
        sample.element === null ||
        !hasCoarsePointerTouchTarget(sample.element),
    )
    .map((sample) => sample.category)
}

export function styleTextOutsideCoarsePointer(): string {
  const css = emittedStyleText()
  let result = ''
  let cursor = 0
  for (;;) {
    const start = css.indexOf(COARSE_POINTER_MEDIA, cursor)
    if (start < 0) return result + css.slice(cursor)
    result += css.slice(cursor, start)
    const open = css.indexOf('{', start)
    if (open < 0) return result
    let depth = 0
    let end = open
    for (; end < css.length; end += 1) {
      if (css[end] === '{') depth += 1
      else if (css[end] === '}') {
        depth -= 1
        if (depth === 0) break
      }
    }
    cursor = end + 1
  }
}

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
    for (const rule of flatRules(body)) {
      if (!rule.selector.includes('.css-')) continue
      if (!matchesSelector(element, rule.selector)) continue
      declarations.set(Number(match[1]), rule.declarations)
    }
  }
  return declarations
}

export function containerMaxWidthOf(element: Element): Map<number, number> {
  const widths = new Map<number, number>()
  for (const [container, declarations] of containerDeclarationsOf(element)) {
    const cap = /max-width:(\d+)px/.exec(declarations)
    if (cap?.[1] !== undefined) widths.set(container, Number(cap[1]))
  }
  return widths
}
