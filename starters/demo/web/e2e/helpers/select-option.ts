/**
 * Choosing an option of an MUI Select, after its menu has finished opening.
 *
 * A Select's menu does not appear in place: the Popover's transition grows the
 * paper from a scaled-down origin to its final size, and every item's box
 * moves while it runs. Playwright's click actionability samples the item's box
 * over two consecutive animation frames and can call it stable while the grow
 * is still moving it, so a click issued then presses down where the item was
 * at that instant and releases after the item has moved. The two events land
 * on different items, the click resolves on their common ancestor, and the
 * selection is lost -- or worse, the release lands on the neighbouring item
 * and chooses it. The grow lasts only a few hundred milliseconds and the
 * window depends on the machine's load, so the race loses a journey
 * intermittently rather than every time.
 *
 * The transition has one deterministic end: MUI's Grow sets the paper's inline
 * style to `transform: none; opacity: 1` when it enters the `entered` state.
 * While the grow runs the transform is a scale matrix and the items move; once
 * it is `none` the paper is at its final geometry, so the click can land
 * nowhere but the chosen item. The helper waits for those two computed styles
 * instead of sampling a box, which turns a timing guess into a single
 * observable state the transition itself publishes. The menu is then asserted
 * closed and the closed control is asserted to read the chosen option's text.
 *
 * `reducedMotion: 'reduce'` in the Playwright config would make every journey
 * run the reduced-motion render path instead of the default user's, which
 * hides this class of race rather than fixing the test; the helper waits for
 * the animation the default user actually sees.
 *
 * The trigger must be addressed by its combobox role. `getByLabel` also
 * matches the open menu, because MUI labels the listbox with the same element;
 * a locator that resolves to two nodes fails in strict mode once the menu has
 * opened.
 *
 * Callers that do not fit the default assertion say so explicitly:
 *
 * - A selection that re-renders the control's text in another language (the
 *   interface-language switch) passes `selectedText: null` and asserts the new
 *   label at the call site, where the product's own rendered option supplies
 *   it. A control that keeps an empty value (MUI renders a zero-width space
 *   for a cleared Select) does the same.
 * - An option that renders a richer label than the closed control -- the
 *   permission picker's options carry a description and a risk badge, while
 *   the control shows the permission code -- passes that code as
 *   `selectedText`.
 * - An option whose text is a regular expression still works: the helper reads
 *   the matched option's own text and asserts the control shows that text, so
 *   the expected value stays the product's rendering.
 */
import { expect, type Locator } from '@playwright/test'

import { scaledTimeout } from '../fixtures/time-scale'

/** The bound for each wait this helper performs. */
const SETTLE_BOUND_MS = 5_000

/**
 * The menu paper that holds `listbox`. Menu composes Popover's paper, so the
 * element carries both `MuiPopover-paper` and `MuiMenu-paper`; addressing it as
 * the listbox's ancestor keeps a second, unrelated popover on the page from
 * matching.
 */
function menuPaper(listbox: Locator): Locator {
  return listbox.locator(
    'xpath=ancestor::*[contains(concat(" ", normalize-space(@class), " "), " MuiPopover-paper ")]',
  )
}

/**
 * Open a Select through `trigger`, wait for its menu to finish opening, choose
 * `option`, then assert the menu closed and the control reads the choice.
 *
 * `selectedText` defaults to the chosen option's own rendered text; pass an
 * explicit value when the product renders the closed control differently, or
 * `null` when the caller asserts it itself.
 */
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
  // Grow's `entered` state, published as the paper's inline style: a running
  // grow still has a scale matrix here, and the items are moving with it.
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
