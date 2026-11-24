import { describe, expect, it } from 'vitest'
import { createI18n, registerNamespace } from '@speed/i18n'
import { AUTH_UI_NAMESPACE, authUiResources } from './resources.js'
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

function leafPaths(value: unknown, prefix = ''): string[] {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    return [prefix]
  }
  return Object.entries(value as Record<string, unknown>).flatMap(
    ([key, child]) => leafPaths(child, prefix === '' ? key : `${prefix}.${key}`),
  )
}

describe('auth-ui resources', () => {
  it('register cleanly on a fresh instance (parity enforced by registerNamespace)', () => {
    const i18n = newInstance()
    expect(() =>
      registerNamespace(i18n, AUTH_UI_NAMESPACE, authUiResources),
    ).not.toThrow()
  })

  it('cover exactly the canonical supported languages, both files, no extras', () => {
    expect(Object.keys(authUiResources).sort()).toEqual(['en-US', 'zh-CN'])
  })

  it('document the shipped sections (one per component family with built-in text)', () => {
    const sections = Object.keys(authUiResources['zh-CN']!).sort()
    expect(sections).toEqual([
      'acceptInvitation',
      'discovery',
      'enterprise',
      'errors',
      'impersonation',
      'mode',
      'passkey',
      'passwordReset',
      'passwordSignIn',
      'register',
      'sessionEnded',
      'signInFlow',
      'signOut',
      'smsSignIn',
      'social',
      'socialCallback',
      'throttle',
    ])
  })

  it('carry the same passwordReset leaves in both languages', () => {
    expect(leafPaths(zhCN.passwordReset).sort()).toEqual(
      leafPaths(enUS.passwordReset).sort(),
    )
    expect(leafPaths(zhCN.passwordSignIn)).toContain('forgotPassword')
    expect(leafPaths(enUS.passwordSignIn)).toContain('forgotPassword')
  })

  it('render the zh-CN bundle verbatim through the registered namespace', () => {
    const i18n = newInstance()
    registerNamespace(i18n, AUTH_UI_NAMESPACE, authUiResources)
    expect(
      i18n.t('passwordSignIn.identifierLabel', { ns: AUTH_UI_NAMESPACE }),
    ).toBe(zhCN.passwordSignIn.identifierLabel)
    expect(
      i18n.t('social.provider.feishu', { ns: AUTH_UI_NAMESPACE }),
    ).toBe(zhCN.social.provider.feishu)
  })

  it('render the en-US bundle verbatim after a language switch', async () => {
    const i18n = newInstance()
    registerNamespace(i18n, AUTH_UI_NAMESPACE, authUiResources)
    await i18n.changeLanguage('en-US')
    expect(
      i18n.t('passwordSignIn.identifierLabel', { ns: AUTH_UI_NAMESPACE }),
    ).toBe(enUS.passwordSignIn.identifierLabel)
    expect(
      i18n.t('signInFlow.submit', { ns: AUTH_UI_NAMESPACE }),
    ).toBe(enUS.signInFlow.submit)
  })

  it('interpolate the SMS sent notice from each bundle template', async () => {
    const i18n = newInstance()
    registerNamespace(i18n, AUTH_UI_NAMESPACE, authUiResources)
    const phone = '+8613800138000'
    expect(
      i18n.t('smsSignIn.sentNotice', { ns: AUTH_UI_NAMESPACE, phone }),
    ).toBe(zhCN.smsSignIn.sentNotice.replace('{{phone}}', phone))
    await i18n.changeLanguage('en-US')
    expect(
      i18n.t('smsSignIn.sentNotice', { ns: AUTH_UI_NAMESPACE, phone }),
    ).toBe(enUS.smsSignIn.sentNotice.replace('{{phone}}', phone))
  })

  it('resolve nested errors-section keys to dedicated text, never the fallback', () => {
    const i18n = newInstance()
    registerNamespace(i18n, AUTH_UI_NAMESPACE, authUiResources)
    const fallback = i18n.t('errors.unknown', { ns: AUTH_UI_NAMESPACE })
    for (const key of [
      'errors.authn.login_failed',
      'errors.authn.flow_invalid',
      'errors.authn.method_disabled',
      'errors.authn.throttled',
      'errors.authn.unsupported_region',
      'errors.authn.unavailable',
      'errors.client.network',
    ]) {
      expect(i18n.t(key, { ns: AUTH_UI_NAMESPACE })).not.toBe(fallback)
    }
  })

  it('refuse double registration on the same instance (host registers once)', () => {
    const i18n = newInstance()
    registerNamespace(i18n, AUTH_UI_NAMESPACE, authUiResources)
    expect(() =>
      registerNamespace(i18n, AUTH_UI_NAMESPACE, authUiResources),
    ).toThrow(/already registered/)
  })
})
