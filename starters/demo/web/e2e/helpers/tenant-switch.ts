// The host passes announcement="visually-hidden": the confirmation stays a role=status region clipped to a 1x1 box. Assert both the exact text node and the 1px bound.

import { expect, type Page } from '@playwright/test'

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
