/**
 * The tenant switcher's switch confirmation, asserted as the clipped
 * announcement it is.
 *
 * The host passes `announcement="visually-hidden"` to the switcher, so the
 * confirmation stays in a `role="status"` region that keeps announcing the
 * switch while taking the kit's clip recipe: a 1x1 box that paints nothing.
 * The assertion proves both halves of that contract -- the text is still one
 * exact node in the document, and its box stays within the 1px bound -- so a
 * regression back to an inline confirmation, whose span grows to the
 * sentence's width and line height, fails here.
 */

import { expect, type Page } from '@playwright/test'

/**
 * Assert one exact copy string is present in the document and clipped to a
 * box no larger than one pixel in either dimension.
 */
export async function expectClippedAnnouncement(
  page: Page,
  text: string,
): Promise<void> {
  const announcement = page.getByText(text, { exact: true })
  await expect(announcement).toHaveCount(1)
  const box = await announcement.boundingBox()
  expect(box, 'the clipped announcement is in the document').not.toBeNull()
  expect(box?.width ?? Infinity).toBeLessThanOrEqual(1)
  expect(box?.height ?? Infinity).toBeLessThanOrEqual(1)
}
