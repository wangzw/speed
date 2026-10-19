/**
 * applyDocumentMetadata: the entry document's title follows the active
 * language, at boot and across a switch, so the tab names the brand instead
 * of a build-time placeholder.
 *
 * `<html lang>` belongs to the app-entry assembly (SpeedAppDefinition's
 * `documentLanguage`), so this module must not touch it: the suite pins that
 * boundary as well as the title.
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
  it('names the brand in the title, in the active language', () => {
    const i18n = demoI18n('zh-CN')
    applyDocumentMetadata(i18n)

    expect(document.title).toBe(zhCN.app.title)
    expect(document.title).not.toBe('demo')
  })

  it('follows a language switch', async () => {
    const i18n = demoI18n('zh-CN')
    applyDocumentMetadata(i18n)

    await i18n.changeLanguage('en-US')
    expect(document.title).toBe(enUS.app.title)
  })

  it('leaves the document language to the assembly', () => {
    // The assembly owns <html lang> (a host that opts out of its binding
    // manages it itself). A second writer here would fight it on every
    // switch, so the title helper must not touch the attribute.
    document.documentElement.lang = 'fr'
    applyDocumentMetadata(demoI18n('zh-CN'))
    expect(document.documentElement.lang).toBe('fr')
  })
})
