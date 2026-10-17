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

/**
 * Whether `element` itself carries a coarse-pointer rule that raises its
 * hit area to the 44x44 floor -- i.e. the `touchTargetSx` recipe was
 * applied to this control.
 *
 * The check is per element: one of the emotion classes on the element
 * must be named in a rule inside an `@media (pointer: coarse)` block,
 * and that rule must set both the minimum width and height. Gating one
 * control on a page therefore does not satisfy the check for another,
 * which is what makes the assertion a proof that *this* control was
 * wired up rather than that the page emitted the media query somewhere.
 */
export function hasCoarsePointerTouchTarget(element: Element): boolean {
  const css = emittedStyleText()
  const classes = Array.from(element.classList).filter((name) =>
    name.startsWith('css-'),
  )
  if (classes.length === 0) return false
  for (const tail of css.split('@media (pointer: coarse)').slice(1)) {
    const next = tail.indexOf('@media')
    const mediaBody = next < 0 ? tail : tail.slice(0, next)
    for (const className of classes) {
      const at = mediaBody.indexOf(`.${className}`)
      if (at < 0) continue
      const open = mediaBody.indexOf('{', at)
      const close = open < 0 ? -1 : mediaBody.indexOf('}', open)
      if (open < 0 || close < 0) continue
      const rule = mediaBody.slice(open + 1, close)
      if (
        rule.includes('min-width:44px') &&
        rule.includes('min-height:44px')
      ) {
        return true
      }
    }
  }
  return false
}
