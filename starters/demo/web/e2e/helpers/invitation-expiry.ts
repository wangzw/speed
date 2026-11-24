// The invitation form reads its own clock at submit, so the deadline lies between the two instants a spec records around that action.
import { expect, type Locator } from '@playwright/test'

export const CHOSEN_LIFETIME_MS = 30 * 24 * 60 * 60 * 1000

const expiryFormatter = new Intl.DateTimeFormat('en-US', {
  dateStyle: 'medium',
  timeStyle: 'short',
  timeZone: 'UTC',
})

// Both endpoints round down to their minute and every minute of the span is formatted; a span shorter than a minute can still cross the boundary at its far end.
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

export function normalizedText(text: string): string {
  return text.replace(/\s+/g, ' ').trim()
}

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
