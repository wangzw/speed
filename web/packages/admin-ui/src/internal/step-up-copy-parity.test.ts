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

const RESUME_PROMISE: Record<Language, string> = {
  'zh-CN': '回到原处继续',
  'en-US': 'come back here to continue',
}

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

function accountSentence(language: Language): string {
  const sentence = readBundle('account-ui', language).mfa?.stepUp?.cannotElevate
  if (sentence === undefined) {
    throw new Error(`account-ui ${language} has no mfa.stepUp.cannotElevate`)
  }
  return sentence
}

function adminSentence(language: Language): string {
  const sentence = readBundle('admin-ui', language).stepUp?.cannotElevate
  if (sentence === undefined) {
    throw new Error(`admin-ui ${language} has no stepUp.cannotElevate`)
  }
  return sentence
}

function causeClause(sentence: string): string {
  const match = /^[^。.]*[。.]/.exec(sentence)
  if (match === null) {
    throw new Error(`the sentence carries no first sentence: ${sentence}`)
  }
  return match[0]
}

describe('the shared unavailable explanation', () => {
  it.each(LANGUAGES)(
    'states the same causes in account-ui and admin-ui (%s)',
    (language) => {
      expect(causeClause(accountSentence(language))).toBe(
        causeClause(adminSentence(language)),
      )
    },
  )

  it.each(LANGUAGES)(
    'keeps the resume promise in account-ui alone (%s)',
    (language) => {
      const clause = causeClause(adminSentence(language))
      expect(accountSentence(language).startsWith(clause)).toBe(true)
      expect(adminSentence(language).startsWith(clause)).toBe(true)
      expect(accountSentence(language)).toContain(RESUME_PROMISE[language])
      expect(adminSentence(language)).not.toContain(RESUME_PROMISE[language])
    },
  )
})
