/**
 * Reads the CSS text emotion (MUI's styling engine) has injected into
 * the document for the current render, across every <style> tag it
 * owns.
 *
 * MUI's `sx` prop compiles most values into emotion-generated
 * stylesheet rules rather than an inline `style` attribute, and a
 * breakpoint-keyed value (e.g. `{ xs: '1fr', sm: 'repeat(2, 1fr)' }`)
 * compiles to a base rule plus an `@media` rule. jsdom evaluates
 * neither real layout nor `@media` conditions, so `getComputedStyle`
 * (the basis of jest-dom's `toHaveStyle`) cannot see which side of a
 * breakpoint is "active" -- there is no real viewport for either side
 * to be active at. Reading the actual generated CSS text instead is a
 * property/snapshot proof that the intended declaration (base value
 * *and* its `@media` guard) was wired into the page; it does not prove
 * the layout looks correct at any real viewport width, which jsdom
 * cannot render at all.
 */
export function emittedStyleText(): string {
  return Array.from(document.querySelectorAll('style'))
    .map((style) => style.textContent ?? '')
    .join('\n')
}

// The media feature the touch-target floor is gated on. Kept as a literal
// because this package is a dev-only helper the presentational packages
// depend on, not the reverse: it cannot import the ui-kit constant that
// spells the same string in shipped code.
const COARSE_POINTER_MEDIA = '@media (pointer: coarse)'

/**
 * The body text of every `@media (pointer: coarse)` block in `css`. A
 * media block can contain nested braces (a rule with its own nested
 * selector), so the end is found by matching braces, not by the first
 * `}`.
 */
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

/**
 * The top-level `selector { declarations }` pairs of a stylesheet body.
 * A rule's own nested blocks (`&:hover { ... }`) stay inside its
 * declaration text rather than becoming separate entries, so a caller
 * testing the rule's own declarations reads exactly those.
 */
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

/**
 * Whether `element` matches `selector`, treating anything the selector
 * engine rejects (a pseudo-element, a malformed fragment) as a non-match
 * rather than an error.
 */
function matchesSelector(element: Element, selector: string): boolean {
  try {
    return element.matches(selector)
  } catch {
    return false
  }
}

/**
 * Whether `element` itself is covered by a coarse-pointer rule that
 * raises its hit area to the 44x44 floor.
 *
 * The rule is matched by its selector against the element, so a compound
 * selector only counts for the element it really selects (a clickable
 * chip's `&.MuiChip-clickable` rule does not cover a status chip that
 * shares the emotion class prefix), and both the minimum width and the
 * minimum height must be set in the same rule. A rule written for another
 * control on the page therefore does not satisfy the check for this one,
 * which is what makes the assertion a proof that *this* control carries
 * the floor -- whether it came from the theme's component overrides, from
 * `touchTargetSx` through `sx`, or from a styled wrapper.
 */
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
  /** The control category the sample stands for ("primary button", ...). */
  readonly category: string
  /** The rendered control, or null when the fixture failed to render it. */
  readonly element: Element | null
}

/**
 * The categories whose sample does not carry the coarse-pointer floor.
 * An empty list means every sampled control category reached 44x44 at
 * once -- the category-level form of `hasCoarsePointerTouchTarget`, so a
 * suite proves the whole set rather than a hand-picked element.
 */
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

/**
 * The page's CSS with every `@media (pointer: coarse)` block removed --
 * what a precise pointer sees of the injected styles. A test can assert
 * that a touch-only declaration (the 44px floor) does not appear here:
 * the floor must live entirely inside the media feature, so the desktop
 * rendering is untouched.
 */
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

/**
 * The declarations each `@container (min-width: Npx)` rule gives
 * `element`, keyed by N. Container-keyed styling is pure CSS emitted
 * into the stylesheet, and jsdom evaluates no container query, so the
 * declared value can only be read from the generated rules -- matched
 * against the element so a sibling cell's rule is not mistaken for this
 * one. The `@` baseline rule appears here as the key 0, which is how a
 * cap that applies below a breakpoint is read.
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
    for (const rule of flatRules(body)) {
      if (!rule.selector.includes('.css-')) continue
      if (!matchesSelector(element, rule.selector)) continue
      declarations.set(Number(match[1]), rule.declarations)
    }
  }
  return declarations
}

/**
 * The max-width an element declares, keyed by the container width the
 * declaration applies from. A shared primary-column cap therefore reads
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
