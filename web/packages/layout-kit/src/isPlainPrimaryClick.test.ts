import { createElement } from 'react'
import type { MouseEvent as ReactMouseEvent } from 'react'
import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { isPlainPrimaryClick } from './isPlainPrimaryClick.js'

function clickOn(target: Element, init: MouseEventInit = {}): MouseEvent {
  const event = new MouseEvent('click', {
    button: 0,
    bubbles: true,
    cancelable: true,
    ...init,
  })
  target.dispatchEvent(event)
  return event
}

function elementAround(tagName: string, parent?: Element): HTMLElement {
  const element = document.createElement(tagName)
  parent?.appendChild(element)
  return element
}

describe('isPlainPrimaryClick', () => {
  it('accepts a primary-button click with no modifiers', () => {
    const event = clickOn(elementAround('button'))
    expect(event.button).toBe(0)
    expect(isPlainPrimaryClick(event)).toBe(true)
  })

  it('accepts an event that carries no button value', () => {
    const event = {
      defaultPrevented: false,
      ctrlKey: false,
      metaKey: false,
      shiftKey: false,
      altKey: false,
      target: null,
    } as unknown as MouseEvent
    expect(isPlainPrimaryClick(event)).toBe(true)
  })

  it.each([1, 2, 3])('rejects a non-primary button (%i)', (button) => {
    const event = clickOn(elementAround('button'), { button })
    expect(isPlainPrimaryClick(event)).toBe(false)
  })

  it.each([
    { name: 'ctrl', init: { ctrlKey: true } },
    { name: 'meta', init: { metaKey: true } },
    { name: 'shift', init: { shiftKey: true } },
    { name: 'alt', init: { altKey: true } },
  ] as const)('rejects a $name-click, which asks the browser for a new tab or window', ({ init }) => {
    const event = clickOn(elementAround('a'), init)
    expect(isPlainPrimaryClick(event)).toBe(false)
  })

  it('accepts a click on a plain anchor', () => {
    const event = clickOn(elementAround('a'))
    expect(isPlainPrimaryClick(event)).toBe(true)
  })

  it('rejects a _blank anchor, which opens a new browsing context', () => {
    const anchor = elementAround('a')
    anchor.setAttribute('target', '_blank')
    expect(isPlainPrimaryClick(clickOn(anchor))).toBe(false)
  })

  it('matches the _blank keyword case-insensitively, as browsers do', () => {
    const anchor = elementAround('a')
    anchor.setAttribute('target', '_BLANK')
    expect(isPlainPrimaryClick(clickOn(anchor))).toBe(false)
  })

  it('rejects a download anchor, whose activation saves a file instead of navigating', () => {
    const anchor = elementAround('a')
    anchor.setAttribute('download', '')
    expect(isPlainPrimaryClick(clickOn(anchor))).toBe(false)
  })

  it('judges a click on a descendant of the anchor by the anchor itself', () => {
    const anchor = elementAround('a')
    anchor.setAttribute('target', '_blank')
    const child = elementAround('span', anchor)
    expect(isPlainPrimaryClick(clickOn(child))).toBe(false)
  })

  it('rejects an event another handler already claimed', () => {
    const target = elementAround('a')
    const event = new MouseEvent('click', {
      button: 0,
      bubbles: true,
      cancelable: true,
    })
    event.preventDefault()
    target.dispatchEvent(event)
    expect(event.defaultPrevented).toBe(true)
    expect(isPlainPrimaryClick(event)).toBe(false)
  })

  it('reads a React synthetic event: accepts a plain click', () => {
    let verdict: boolean | null = null
    render(
      createElement(
        'a',
        {
          href: '/reports',
          onClick: (event: ReactMouseEvent) => {
            verdict = isPlainPrimaryClick(event)
            event.preventDefault()
          },
        },
        'Reports',
      ),
    )
    fireEvent.click(screen.getByRole('link', { name: 'Reports' }))
    expect(verdict).toBe(true)
  })

  it('reads a React synthetic event: rejects a modified click', () => {
    let verdict: boolean | null = null
    render(
      createElement(
        'a',
        {
          href: '/reports',
          onClick: (event: ReactMouseEvent) => {
            verdict = isPlainPrimaryClick(event)
            event.preventDefault()
          },
        },
        'Reports',
      ),
    )
    fireEvent.click(screen.getByRole('link', { name: 'Reports' }), {
      ctrlKey: true,
    })
    expect(verdict).toBe(false)
  })

  it('reads a React synthetic event: rejects a _blank anchor activated by a real pointer click', async () => {
    let verdict: boolean | null = null
    render(
      createElement(
        'a',
        {
          target: '_blank',
          onClick: (event: ReactMouseEvent) => {
            verdict = isPlainPrimaryClick(event)
            event.preventDefault()
          },
        },
        'Reports',
      ),
    )
    await userEvent.setup().click(screen.getByText('Reports'))
    expect(verdict).toBe(false)
  })
})
