/**
 * StepUpChallenge in a real browser: the dialog hands focus back to the
 * control that opened it.
 */
import { useState } from 'react'
import { describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import { page, userEvent } from 'vitest/browser'
import type { AuthSession } from '@speed/auth-core'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }
import { makeRealClientRig } from '../../test-utils/real-client.js'
import { renderWithProviders } from '../../test-utils/render.js'
import { StepUpChallenge } from './step-up-challenge.js'

function describeActiveElement(): string {
  const active = document.activeElement
  if (active === null) {
    return 'null'
  }
  const parts = [active.tagName.toLowerCase()]
  const role = active.getAttribute('role')
  const label = active.getAttribute('aria-label')
  const text = (active.textContent ?? '').replace(/\s+/g, ' ').trim().slice(0, 80)
  if (role !== null) {
    parts.push(`role="${role}"`)
  }
  if (label !== null) {
    parts.push(`aria-label="${label}"`)
  }
  if (text !== '') {
    parts.push(`text="${text}"`)
  }
  return parts.join(' ')
}

function Harness({ session }: { readonly session: AuthSession }) {
  const [open, setOpen] = useState(false)
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>
        Open challenge
      </button>
      <StepUpChallenge
        open={open}
        reason="sso"
        session={session}
        onSuccess={() => undefined}
        onCancel={() => setOpen(false)}
      />
    </>
  )
}

async function openChallenge(): Promise<{
  readonly opener: HTMLElement
  readonly dialog: HTMLElement
  readonly container: HTMLElement
}> {
  await page.viewport(1200, 900)
  const rig = makeRealClientRig(() => {
    throw new Error('the SSO challenge issues no request')
  })
  rig.store.set('access-1')
  const { container } = renderWithProviders(<Harness session={rig.session} />)

  const opener = await screen.findByRole('button', { name: 'Open challenge' })
  await userEvent.click(opener)
  const dialog = await screen.findByRole('dialog')
  return { opener, dialog, container }
}

const WALK_CONTROL_SELECTOR =
  'a[href], button, input, select, textarea, [tabindex]'

/** Presses `keys` through one full cycle of `root`'s controls, failing at
 * the first step that lands focus outside `root`. */
async function walkFocusTrap(root: HTMLElement, keys: string): Promise<void> {
  const controls = [
    ...root.querySelectorAll<HTMLElement>(WALK_CONTROL_SELECTOR),
  ].filter(
    (control) =>
      control.tabIndex >= 0 &&
      !control.hasAttribute('disabled') &&
      control.getClientRects().length > 0,
  )
  expect(controls.length).toBeGreaterThan(0)
  expect(
    document.activeElement instanceof HTMLElement
      && root.contains(document.activeElement),
  ).toBe(true)
  for (let step = 0; step <= controls.length; step += 1) {
    await userEvent.keyboard(keys)
    const active = document.activeElement
    expect(
      active instanceof HTMLElement && root.contains(active),
      `step ${step} of the ${keys} walk left the dialog`,
    ).toBe(true)
  }
}

describe('StepUpChallenge focus in Chromium', () => {
  it('returns focus to the triggering control after Escape closes the dialog', async () => {
    const { opener } = await openChallenge()

    await userEvent.keyboard('{Escape}')

    await waitFor(() =>
      expect(
        document.activeElement,
        `focus after Escape; document.activeElement is ${describeActiveElement()}`,
      ).toBe(opener),
    )
  })

  it('marks the page inert and holds the Tab cycle inside the dialog', async () => {
    const { dialog, container } = await openChallenge()

    await waitFor(() => expect(container).toHaveAttribute('inert'))
    const root = dialog.closest('.MuiModal-root')
    expect(root).not.toBeNull()
    expect(root).not.toHaveAttribute('inert')
    await waitFor(() =>
      expect(dialog.contains(document.activeElement)).toBe(true),
    )

    await walkFocusTrap(dialog, '{Tab}')
    await walkFocusTrap(dialog, '{Shift>}{Tab}{/Shift}')
  })

  it('returns focus to the triggering control after the cancel button closes the dialog', async () => {
    const { opener, dialog } = await openChallenge()

    await userEvent.click(
      within(dialog).getByRole('button', {
        name: zhCN.mfa.stepUp.cancelLabel,
      }),
    )

    await waitFor(() =>
      expect(
        document.activeElement,
        `focus after cancel; document.activeElement is ${describeActiveElement()}`,
      ).toBe(opener),
    )
  })
})
