/**
 * Structural guard: the two step-up dialogs explain the same unavailable
 * phase with the same causes.
 *
 * `account-ui` (`mfa.stepUp.cannotElevate`) and `console-ui`
 * (`stepUp.cannotElevate`) each own the sentence their step-up dialog
 * shows when the deployment cannot raise the session -- the account
 * surface then adds how the new sign-in resumes the refused write. Only
 * the leading cause clause is shared, so the two sentences are not
 * identical by design; that clause is the same description of the same
 * server answer and must not drift apart.
 *
 * The sentence is read from both locale bundles, not written here: the
 * package that narrows the cause again -- naming one reason where the
 * server answer has several -- fails with both texts printed.
 */

import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const packagesDir = join(
  dirname(fileURLToPath(import.meta.url)),
  '..',
  '..',
  '..',
)

const LANGUAGES = ['zh-CN', 'en-US'] as const

type Language = (typeof LANGUAGES)[number]

/** The leaf each package carries under its own namespace. */
interface Bundle {
  readonly mfa?: { readonly stepUp?: { readonly cannotElevate?: string } }
  readonly stepUp?: { readonly cannotElevate?: string }
}

function readBundle(packageName: string, language: Language): Bundle {
  return JSON.parse(
    readFileSync(
      join(packagesDir, packageName, 'src', 'locales', `${language}.json`),
      'utf8',
    ),
  ) as Bundle
}

/** The unavailable sentence account-ui renders. */
function accountSentence(language: Language): string {
  const sentence = readBundle('account-ui', language).mfa?.stepUp?.cannotElevate
  if (sentence === undefined) {
    throw new Error(`account-ui ${language} has no mfa.stepUp.cannotElevate`)
  }
  return sentence
}

/** The unavailable sentence console-ui renders. */
function consoleSentence(language: Language): string {
  const sentence = readBundle('console-ui', language).stepUp?.cannotElevate
  if (sentence === undefined) {
    throw new Error(`console-ui ${language} has no stepUp.cannotElevate`)
  }
  return sentence
}

/**
 * The leading cause clause of a sentence: the first sentence, which both
 * dialogues share. A full stop ends it in either language.
 */
function causeClause(sentence: string): string {
  const match = /^[^。.]*[。.]/.exec(sentence)
  if (match === null) {
    throw new Error(`the sentence carries no first sentence: ${sentence}`)
  }
  return match[0]
}

describe('the shared unavailable explanation', () => {
  it.each(LANGUAGES)(
    'states the same causes in account-ui and console-ui (%s)',
    (language) => {
      expect(causeClause(accountSentence(language))).toBe(
        causeClause(consoleSentence(language)),
      )
    },
  )

  it.each(LANGUAGES)(
    'lets account-ui add the resume sentence after the shared clause (%s)',
    (language) => {
      // Both sentences open with the shared cause clause; account-ui then
      // continues with how the new sign-in resumes the refused write, while
      // console-ui ends the session without a resume record and closes with
      // the shorter re-login instruction.
      const clause = causeClause(consoleSentence(language))
      expect(accountSentence(language).startsWith(clause)).toBe(true)
      expect(consoleSentence(language).startsWith(clause)).toBe(true)
      expect(accountSentence(language).length).toBeGreaterThan(
        consoleSentence(language).length,
      )
    },
  )
})
