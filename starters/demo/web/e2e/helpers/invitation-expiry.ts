/**
 * The expiry a chosen invitation lifetime renders as, computed by the test
 * rather than read back from either page.
 *
 * The invitation forms read their own clock when submitted, so the deadline
 * lies between the two instants a spec records around that action. Formatting
 * that span's minutes from the chosen lifetime lets a spec assert the
 * deadline the product computed; a lifetime the product ignored renders
 * outside the set.
 */
import { expect, type Locator } from '@playwright/test'

/** The lifetime both invitation journeys choose: thirty days. */
export const CHOSEN_LIFETIME_MS = 30 * 24 * 60 * 60 * 1000

const expiryFormatter = new Intl.DateTimeFormat('en-US', {
  dateStyle: 'medium',
  timeStyle: 'short',
  timeZone: 'UTC',
})

/**
 * The expiry strings the chosen lifetime can render as when the issue landed
 * between `from` and `to`.
 *
 * Both endpoints are formatted directly, and so is every minute the span
 * covers, with each endpoint rounded down to its minute first. The recorded
 * span can be shorter than a minute: a deadline that crosses the minute
 * boundary at its far end must still be in the set, which a loop that steps
 * from `from` alone would miss.
 */
export function expiryChoices(from: number, to: number): string[] {
  const first = Math.min(from, to)
  const last = Math.max(from, to)
  const choices = new Set<string>([
    expiryFormatter.format(new Date(first + CHOSEN_LIFETIME_MS)),
    expiryFormatter.format(new Date(last + CHOSEN_LIFETIME_MS)),
  ])
  const firstMinute = Math.floor(first / 60_000) * 60_000
  const lastMinute = Math.floor(last / 60_000) * 60_000
  for (let at = firstMinute; at <= lastMinute; at += 60_000) {
    choices.add(expiryFormatter.format(new Date(at + CHOSEN_LIFETIME_MS)))
  }
  return [...choices]
}

/** Whitespace-normalized text, so two engines' spacing compares equal. */
export function normalizedText(text: string): string {
  return text.replace(/\s+/g, ' ').trim()
}

/** Asserts the locator renders one of the computed expiry strings. */
export async function expectOneOfText(
  locator: Locator,
  choices: readonly string[],
): Promise<void> {
  const allowed = choices.map(normalizedText)
  await expect(async () => {
    const text = normalizedText((await locator.textContent()) ?? '')
    expect(allowed, `rendered expiry ${JSON.stringify(text)}`).toContain(text)
  }).toPass()
}
