/**
 * applyDocumentMetadata: the entry document's language and title follow the
 * active language, at boot and across a switch -- so a Chinese interface is
 * announced as Chinese (WCAG 3.1.1) and the tab names the brand instead of a
 * build-time placeholder.
 */

import { describe, expect, it } from 'vitest'
import { createI18n, registerNamespace } from '@speed/i18n'
import enUS from './locales/en-US.json' with { type: 'json' }
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import { DEMO_NAMESPACE, demoResources } from './resources.js'
import { applyDocumentMetadata } from './document-metadata.js'

/** A bilingual instance over the demo namespace, starting in `language`. */
function demoI18n(language: string) {
  const i18n = createI18n({
    supportedLanguages: ['zh-CN', 'en-US'],
    defaultLanguage: language,
    storage: null,
    urlParameterName: null,
    navigatorLanguages: [],
  })
  registerNamespace(i18n, DEMO_NAMESPACE, demoResources)
  return i18n
}

describe('applyDocumentMetadata', () => {
  it('announces the active language and names the brand in the title', () => {
    const i18n = demoI18n('zh-CN')
    applyDocumentMetadata(i18n)

    expect(document.documentElement.lang).toBe('zh-CN')
    expect(document.title).toBe(zhCN.app.title)
    expect(document.title).not.toBe('demo')
  })

  it('follows a language switch', async () => {
    const i18n = demoI18n('zh-CN')
    applyDocumentMetadata(i18n)

    await i18n.changeLanguage('en-US')
    expect(document.documentElement.lang).toBe('en-US')
    expect(document.title).toBe(enUS.app.title)
  })
})
