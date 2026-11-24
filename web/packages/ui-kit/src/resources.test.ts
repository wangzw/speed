
import { describe, expect, it } from 'vitest'
import { createI18n, registerNamespace } from '@speed/i18n'
import { UI_KIT_NAMESPACE, uiKitResources } from './resources.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import enUS from './locales/en-US.json' with { type: 'json' }

function newInstance(): ReturnType<typeof createI18n> {
  return createI18n({
    storage: null,
    urlParameterName: null,
    searchParams: null,
    navigatorLanguages: [],
    defaultLanguage: 'zh-CN',
  })
}

describe('ui-kit resources', () => {
  it('register cleanly on a fresh instance (parity enforced by registerNamespace)', () => {
    const i18n = newInstance()
    expect(() => registerNamespace(i18n, UI_KIT_NAMESPACE, uiKitResources)).not.toThrow()
  })

  it('cover exactly the canonical supported languages, both files, no extras', () => {
    expect(Object.keys(uiKitResources).sort()).toEqual(['en-US', 'zh-CN'])
  })

  it('document the shipped sections (one per component family with built-in text)', () => {
    const sections = Object.keys(uiKitResources['zh-CN']!).sort()
    expect(sections).toEqual([
      'confirmDialog',
      'copyButton',
      'dataTable',
      'emptyState',
      'fileUploader',
      'form',
      'humanVerification',
      'oneTimeValuePanel',
      'pageHeader',
      'phoneNumber',
      'refreshButton',
      'sidePanel',
    ])
  })

  it('render the zh-CN bundle verbatim through the registered namespace', () => {
    const i18n = newInstance()
    registerNamespace(i18n, UI_KIT_NAMESPACE, uiKitResources)
    expect(i18n.t('emptyState.empty.title', { ns: UI_KIT_NAMESPACE })).toBe(
      zhCN.emptyState.empty.title,
    )
    expect(i18n.t('confirmDialog.confirmLabel', { ns: UI_KIT_NAMESPACE })).toBe(
      zhCN.confirmDialog.confirmLabel,
    )
  })

  it('render the en-US bundle verbatim after a language switch', async () => {
    const i18n = newInstance()
    registerNamespace(i18n, UI_KIT_NAMESPACE, uiKitResources)
    await i18n.changeLanguage('en-US')
    expect(i18n.t('emptyState.empty.title', { ns: UI_KIT_NAMESPACE })).toBe(
      enUS.emptyState.empty.title,
    )
    expect(i18n.t('dataTable.loading', { ns: UI_KIT_NAMESPACE })).toBe(
      enUS.dataTable.loading,
    )
  })

  it('interpolate the pagination summary from each bundle template', async () => {
    const i18n = newInstance()
    registerNamespace(i18n, UI_KIT_NAMESPACE, uiKitResources)
    const ns = UI_KIT_NAMESPACE
    const render = (template: string) =>
      template.replace('{{from}}', '1').replace('{{to}}', '5').replace('{{count}}', '30')
    expect(i18n.t('dataTable.displayedRows', { ns, from: 1, to: 5, count: 30 })).toBe(
      render(zhCN.dataTable.displayedRows),
    )
    await i18n.changeLanguage('en-US')
    expect(i18n.t('dataTable.displayedRows', { ns, from: 1, to: 5, count: 30 })).toBe(
      render(enUS.dataTable.displayedRows),
    )
  })

  it('refuse double registration on the same instance (host registers once)', () => {
    const i18n = newInstance()
    registerNamespace(i18n, UI_KIT_NAMESPACE, uiKitResources)
    expect(() => registerNamespace(i18n, UI_KIT_NAMESPACE, uiKitResources)).toThrow(
      /already registered/,
    )
  })
})
