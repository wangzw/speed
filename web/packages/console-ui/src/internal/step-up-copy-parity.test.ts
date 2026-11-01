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
 * server answer and must not drift apart. The resume promise belongs to
 * account-ui alone: console-ui ends the session without a resume record,
 * so its sentence must not promise a return.
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

/**
 * The account-only resume promise each language's sentence carries. The
 * console surface ends the session without a resume record, so its sentence
 * must not promise a return.
 */
const RESUME_PROMISE: Record<Language, string> = {
  'zh-CN': '回到原处继续',
  'en-US': 'come back here to continue',
}

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
    'keeps the resume promise in account-ui alone (%s)',
    (language) => {
      // Both sentences open with the shared cause clause. account-ui then
      // promises the new sign-in returns to the same page and resumes the
      // refused write; console-ui records no resume and closes with the
      // re-login instruction alone.
      const clause = causeClause(consoleSentence(language))
      expect(accountSentence(language).startsWith(clause)).toBe(true)
      expect(consoleSentence(language).startsWith(clause)).toBe(true)
      expect(accountSentence(language)).toContain(RESUME_PROMISE[language])
      expect(consoleSentence(language)).not.toContain(RESUME_PROMISE[language])
    },
  )
})
