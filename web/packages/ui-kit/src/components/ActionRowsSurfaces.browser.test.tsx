
import { describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { page } from 'vitest/browser'
import { useForm } from 'react-hook-form'
import { defaultTokens } from '@speed/tokens'
import { renderWithProviders } from '../../test-utils/render.js'
import { ActionButton } from './ActionRow.js'
import { ConfirmDialog } from './ConfirmDialog.js'
import { FormLayout } from './FormLayout.js'
import { PageHeader } from './PageHeader.js'
import { SidePanel } from './SidePanel.js'
import { TotpEnrollmentPanel } from './TotpEnrollmentPanel.js'

const SPACING_UNIT = defaultTokens.spacing.unit

const TOLERANCE = 0.5

function actionRowOf(action: Element): HTMLElement {
  const row = action.closest<HTMLElement>('[data-action-row]')
  expect(row).not.toBeNull()
  return row as HTMLElement
}

async function expectOneActionGroup(
  primary: HTMLElement,
  secondary: HTMLElement,
): Promise<void> {
  await waitFor(() => {
    expect(secondary.getBoundingClientRect().width).toBeGreaterThan(0)
  })
  const row = actionRowOf(primary)
  expect(actionRowOf(secondary)).toBe(row)
  const primaryBox = primary.getBoundingClientRect()
  const secondaryBox = secondary.getBoundingClientRect()
  const rowBox = row.getBoundingClientRect()
  const contentRight =
    rowBox.right - Number.parseFloat(getComputedStyle(row).paddingRight)

  expect(Math.abs(primaryBox.top - secondaryBox.top)).toBeLessThanOrEqual(
    TOLERANCE,
  )
  expect(primaryBox.left).toBeGreaterThanOrEqual(secondaryBox.right)
  expect(primaryBox.left - secondaryBox.right).toBeCloseTo(SPACING_UNIT, 0)
  expect(primaryBox.left).toBeGreaterThan(secondaryBox.left)
  expect(Math.abs(primaryBox.right - contentRight)).toBeLessThanOrEqual(
    TOLERANCE,
  )
}

function FormHarness() {
  const form = useForm<{ name: string }>({ defaultValues: { name: '' } })
  return (
    <FormLayout
      form={form}
      actions={
        <>
          <ActionButton>Cancel</ActionButton>
          <ActionButton emphasis="primary">Save</ActionButton>
        </>
      }
    >
      <input aria-label="Name" />
    </FormLayout>
  )
}

describe('action rows in ui-kit surfaces', () => {
  it('groups the form skeleton actions one token apart, primary rightmost', async () => {
    await page.viewport(900, 700)
    renderWithProviders(
      <div style={{ width: 860 }}>
        <FormHarness />
      </div>,
      { language: 'en-US' },
    )

    const cancel = await screen.findByRole('button', { name: 'Cancel' })
    const save = screen.getByRole('button', { name: 'Save' })
    await expectOneActionGroup(save, cancel)
  })

  it('groups the page header actions one token apart, primary rightmost', async () => {
    await page.viewport(900, 700)
    renderWithProviders(
      <PageHeader
        title="Members"
        actions={
          <>
            <ActionButton>Invite</ActionButton>
            <ActionButton emphasis="primary">Save</ActionButton>
          </>
        }
      />,
      { language: 'en-US' },
    )

    const invite = await screen.findByRole('button', { name: 'Invite' })
    const save = screen.getByRole('button', { name: 'Save' })
    await expectOneActionGroup(save, invite)
  })

  it('groups the TOTP panel actions one token apart, primary rightmost', async () => {
    await page.viewport(900, 700)
    renderWithProviders(
      <div style={{ width: 600 }}>
        <TotpEnrollmentPanel
          qrLabel="Setup code"
          manualToggleLabel="Enter the key manually"
          secretLabel="Setup key"
          copyLabel="Copy setup key"
          copiedLabel="Setup key copied"
          codeLabel="Six-digit code"
          code=""
          onCodeChange={() => undefined}
          secondaryAction={<ActionButton>Cancel</ActionButton>}
          primaryAction={<ActionButton emphasis="primary">Confirm</ActionButton>}
        />
      </div>,
      { language: 'en-US' },
    )

    const cancel = await screen.findByRole('button', { name: 'Cancel' })
    const confirm = screen.getByRole('button', { name: 'Confirm' })
    await expectOneActionGroup(confirm, cancel)
  })

  it('groups a confirmation dialog one token apart, cancel then confirm', async () => {
    await page.viewport(900, 700)
    renderWithProviders(
      <ConfirmDialog
        open
        title="Remove member"
        message="The member loses access."
        cancelLabel="Cancel"
        confirmLabel="Remove"
        onConfirm={() => undefined}
        onCancel={() => undefined}
      />,
      { language: 'en-US' },
    )

    const cancel = await screen.findByRole('button', { name: 'Cancel' })
    const confirm = screen.getByRole('button', { name: 'Remove' })
    await expectOneActionGroup(confirm, cancel)
  })

  it('groups the side panel footer one token apart, primary rightmost', async () => {
    await page.viewport(900, 700)
    renderWithProviders(
      <SidePanel
        open
        title="Account details"
        actions={
          <>
            <ActionButton>Cancel</ActionButton>
            <ActionButton emphasis="primary">Save</ActionButton>
          </>
        }
        onClose={() => undefined}
        onExited={() => undefined}
      >
        Body
      </SidePanel>,
      { language: 'en-US' },
    )

    const cancel = await screen.findByRole('button', { name: 'Cancel' })
    const save = screen.getByRole('button', { name: 'Save' })
    await expectOneActionGroup(save, cancel)
  })
})
