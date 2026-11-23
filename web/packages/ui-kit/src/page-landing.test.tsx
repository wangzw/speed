/**
 * focusPageLanding: the rule that hands focus to the new page after a
 * page change.
 *
 * The suite pins the lookup order -- landmark heading, then the landmark,
 * then a loose heading, then nothing -- the suppression test over hidden,
 * aria-hidden and inert subtrees, and the focus handling: a target with no
 * tabindex gets a temporary `-1` that the first blur removes, while a
 * target that already carries one is left alone.
 */

import { afterEach, describe, expect, it } from 'vitest'
import { focusPageLanding } from './page-landing.js'

function mount(markup: string): void {
  document.body.innerHTML = markup
}

function element<T extends Element = HTMLElement>(selector: string): T {
  const found = document.querySelector<T>(selector)
  if (found === null) {
    throw new Error(`the fixture holds no ${selector}`)
  }
  return found
}

afterEach(() => {
  document.body.innerHTML = ''
})

describe('focusPageLanding', () => {
  it('hands focus to the first level-one heading inside the main landmark', () => {
    mount('<main><h1>Account</h1><button type="button">Save</button></main>')
    const heading = element('h1')

    expect(focusPageLanding(document)).toBe(heading)
    expect(document.activeElement).toBe(heading)
    expect(heading.getAttribute('tabindex')).toBe('-1')
  })

  it('accepts a role-heading at level one, and a role-main landmark', () => {
    mount(
      '<div role="main"><div role="heading" aria-level="1">Account</div></div>',
    )
    const heading = element('[role="heading"]')

    expect(focusPageLanding(document)).toBe(heading)
    expect(document.activeElement).toBe(heading)
    expect(heading.getAttribute('tabindex')).toBe('-1')
  })

  it('prefers the heading inside the landmark over one before it in scope', () => {
    mount('<h1>Outside</h1><main><h1>Inside</h1></main>')
    const inside = element('main h1')

    expect(focusPageLanding(document)).toBe(inside)
    expect(document.activeElement).toBe(inside)
  })

  it('falls back to the main landmark when it holds no level-one heading', () => {
    mount('<main><h2>Section</h2></main>')
    const main = element('main')

    expect(focusPageLanding(document)).toBe(main)
    expect(document.activeElement).toBe(main)
    expect(main.getAttribute('tabindex')).toBe('-1')
  })

  it('takes the scope itself when the scope is the main landmark', () => {
    mount('<main><h1>Account</h1></main>')
    const main = element('main')
    const heading = element('h1')

    expect(focusPageLanding(main)).toBe(heading)
    expect(document.activeElement).toBe(heading)
  })

  it('falls back to the first heading in scope when there is no main landmark', () => {
    mount('<div><h1>Loose</h1><h1>Later</h1></div>')
    const first = element('h1')

    expect(focusPageLanding(document)).toBe(first)
    expect(document.activeElement).toBe(first)
  })

  it('skips headings inside hidden, aria-hidden and inert subtrees', () => {
    mount(`
      <main>
        <h1 hidden>Hidden attribute</h1>
        <div aria-hidden="true"><h1>Aria hidden</h1></div>
        <div inert><h1>Inert</h1></div>
        <h1 aria-hidden="true">Aria hidden itself</h1>
        <h1 id="visible">Visible</h1>
      </main>
    `)
    const visible = element('#visible')

    expect(focusPageLanding(document)).toBe(visible)
    expect(document.activeElement).toBe(visible)
  })

  it('falls back to the landmark when every heading in it is suppressed', () => {
    mount('<main><h1 hidden>Hidden</h1></main>')
    const main = element('main')

    expect(focusPageLanding(document)).toBe(main)
    expect(document.activeElement).toBe(main)
  })

  it('leaves focus where it was and returns null when nothing qualifies', () => {
    mount('<button type="button">Before</button><div><p>Text</p></div>')
    const button = element('button')
    button.focus()

    expect(focusPageLanding(document)).toBeNull()
    expect(document.activeElement).toBe(button)
  })

  it('removes the temporary tabindex on the first blur', () => {
    mount('<main><h1>Account</h1></main><button type="button">Elsewhere</button>')
    const heading = element('h1')
    const button = element('button')

    focusPageLanding(document)
    expect(heading.getAttribute('tabindex')).toBe('-1')

    button.focus()
    expect(heading.hasAttribute('tabindex')).toBe(false)
  })

  it('leaves an existing tabindex on a heading untouched', () => {
    mount('<main><h1 tabindex="0">Account</h1></main><button type="button">Elsewhere</button>')
    const heading = element('h1')
    const button = element('button')

    focusPageLanding(document)
    expect(document.activeElement).toBe(heading)
    expect(heading.getAttribute('tabindex')).toBe('0')

    button.focus()
    expect(heading.getAttribute('tabindex')).toBe('0')
  })

  it('leaves an existing tabindex on the landmark untouched', () => {
    mount('<main tabindex="-1"><h2>Section</h2></main><button type="button">Elsewhere</button>')
    const main = element('main')
    const button = element('button')

    focusPageLanding(document)
    expect(document.activeElement).toBe(main)
    expect(main.getAttribute('tabindex')).toBe('-1')

    button.focus()
    expect(main.getAttribute('tabindex')).toBe('-1')
  })

  it('stops the suppression walk at the scope', () => {
    // The scope is the authority on what is visible: a suppressed ancestor
    // above the scope is outside it, so the scope's own heading still
    // counts -- the main-landmark scope route-kit's private copy uses.
    mount('<div aria-hidden="true"><main><h1>Account</h1></main></div>')
    const main = element('main')
    const heading = element('h1')

    expect(focusPageLanding(main)).toBe(heading)
    expect(document.activeElement).toBe(heading)
  })

  it('returns null and clears the temporary tabindex when the target cannot take focus', () => {
    // A display:none subtree is not a suppressed subtree under the rule,
    // so the landmark is the target; the focus call cannot land, and the
    // rule must undo its temporary attribute and report no landing so the
    // caller can fall back.
    mount(
      '<div style="display: none"><main><h2>Section</h2></main></div><button type="button">Before</button>',
    )
    const main = element('main')
    const button = element('button')
    button.focus()

    expect(focusPageLanding(document)).toBeNull()
    expect(document.activeElement).toBe(button)
    expect(main.hasAttribute('tabindex')).toBe(false)
  })
})
