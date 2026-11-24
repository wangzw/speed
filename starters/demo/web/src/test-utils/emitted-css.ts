export function emittedStyleText(): string {
  return Array.from(document.querySelectorAll('style'))
    .map((style) => style.textContent ?? '')
    .join('\n')
}

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
