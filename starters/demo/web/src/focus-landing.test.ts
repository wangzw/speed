/**
 * The landing rule the demo's two in-place page swaps share.
 *
 * The suite pins the two clauses the rule takes from @speed/ui-kit's
 * `focusPageLanding`: a suppressed subtree is skipped, with the scan bounded
 * by the landmark it searches, and the temporary `tabindex` a heading needs
 * to take focus is removed on its first blur rather than left behind.
 */

import { afterEach, describe, expect, it } from 'vitest'
import { findPageHeading, focusPageEntry } from './focus-landing.js'

function mount(markup: string): void {
  document.body.innerHTML = markup
}

function element<T extends HTMLElement = HTMLElement>(selector: string): T {
  const found = document.querySelector<T>(selector)
  if (found === null) {
    throw new Error(`the fixture holds no ${selector}`)
  }
  return found
}

afterEach(() => {
  document.body.innerHTML = ''
})

describe('focusPageEntry', () => {
  it('skips level-one headings inside hidden, aria-hidden and inert subtrees', () => {
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

    focusPageEntry(document.body)
    expect(document.activeElement).toBe(visible)
  })

  it('falls back to the landmark when every heading inside it is suppressed', () => {
    mount('<main><h1 hidden>Hidden</h1></main>')
    const main = element('main')

    focusPageEntry(document.body)
    expect(document.activeElement).toBe(main)
  })

  it('stops the suppression scan at the element it searches from', () => {
    // The landmark sits inside an aria-hidden wrapper: its own content still
    // counts, because the scan stops at the landmark rather than climbing
    // out of it.
    mount(
      '<div aria-hidden="true"><main><h1 id="inside">Inside</h1></main></div>',
    )
    const heading = element('#inside')

    focusPageEntry(element('main'))
    expect(document.activeElement).toBe(heading)
  })

  it('removes the temporary tabindex on the first blur', () => {
    mount(
      '<main><h1>Account</h1></main><button type="button">Elsewhere</button>',
    )
    const heading = element('h1')
    const button = element('button')

    focusPageEntry(document.body)
    expect(heading.getAttribute('tabindex')).toBe('-1')

    button.focus()
    expect(heading.hasAttribute('tabindex')).toBe(false)
  })

  it('leaves an existing tabindex on the target untouched', () => {
    mount(
      '<main><h1 tabindex="0">Account</h1></main><button type="button">Elsewhere</button>',
    )
    const heading = element('h1')
    const button = element('button')

    focusPageEntry(document.body)
    expect(document.activeElement).toBe(heading)
    expect(heading.getAttribute('tabindex')).toBe('0')

    button.focus()
    expect(heading.getAttribute('tabindex')).toBe('0')
  })

  it('falls back to the container itself when it holds no landmark or heading', () => {
    mount('<div id="gate"><p>Waiting</p></div>')
    const gate = element('#gate')
    gate.setAttribute('tabindex', '-1')

    focusPageEntry(gate)
    expect(document.activeElement).toBe(gate)
  })

  it('lands on the level-one heading once one arrives when there is no main', () => {
    // A gate's pending view holds neither a main landmark nor a heading, so
    // the fallback is the container. The wrapper assertion below also holds
    // on the unfixed lookup; the reproduction is the heading that follows.
    mount('<div id="gate"><p>Waiting</p></div>')
    const gate = element('#gate')
    gate.setAttribute('tabindex', '-1')

    focusPageEntry(gate)
    expect(document.activeElement).toBe(gate)

    const heading = document.createElement('h1')
    heading.textContent = 'Invitation'
    gate.append(heading)
    focusPageEntry(gate)
    expect(document.activeElement).toBe(heading)
  })
})

describe('findPageHeading', () => {
  it('prefers the heading inside the landmark and reports no heading before one arrives', () => {
    mount('<h1>Outside</h1><main><h1 id="inside">Inside</h1></main>')
    expect(findPageHeading(document.body)).toBe(element('#inside'))

    mount('<main><h2>Section</h2></main>')
    expect(findPageHeading(document.body)).toBeNull()
  })
})
