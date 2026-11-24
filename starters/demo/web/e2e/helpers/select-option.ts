// MUI's Grow moves the menu items while it runs, so Playwright's actionability check can call an item stable mid-move and a click then presses and releases on different items. Wait for the paper's inline `transform: none; opacity: 1` (Grow's entered state) before clicking. Address the trigger by its combobox role: getByLabel also matches the open menu's listbox.
import { expect, type Locator } from '@playwright/test'

import { scaledTimeout } from '../fixtures/time-scale'

const SETTLE_BOUND_MS = 5_000

// Menu composes Popover's paper, so address the listbox's Popover ancestor; a second popover must not match.
function menuPaper(listbox: Locator): Locator {
  return listbox.locator(
    'xpath=ancestor::*[contains(concat(" ", normalize-space(@class), " "), " MuiPopover-paper ")]',
  )
}

export async function chooseSelectOption(
  trigger: Locator,
  option: Locator,
  selectedText?: string | RegExp | null,
): Promise<void> {
  await trigger.click()
  const page = trigger.page()
  const listbox = page.getByRole('listbox')
  await expect(listbox).toBeVisible({ timeout: scaledTimeout(SETTLE_BOUND_MS) })
  await expect(option).toBeVisible({ timeout: scaledTimeout(SETTLE_BOUND_MS) })
  const label = ((await option.textContent()) ?? '').trim()
  const paper = menuPaper(listbox)
  await expect(paper).toHaveCSS('transform', 'none', {
    timeout: scaledTimeout(SETTLE_BOUND_MS),
  })
  await expect(paper).toHaveCSS('opacity', '1', {
    timeout: scaledTimeout(SETTLE_BOUND_MS),
  })
  await option.click()
  await expect(
    listbox,
    'the menu did not close, so the option was not selected',
  ).toHaveCount(0, { timeout: scaledTimeout(SETTLE_BOUND_MS) })
  if (selectedText !== null) {
    await expect(trigger).toHaveText(selectedText ?? label, {
      timeout: scaledTimeout(SETTLE_BOUND_MS),
    })
  }
}
