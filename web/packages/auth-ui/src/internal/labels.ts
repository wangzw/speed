/** The sign-in family's label vocabulary for methods and fields. */

import type { AuthnField } from '@speed/auth-core'

const METHOD_LABEL_KEYS: Readonly<Record<string, string>> = {
  password: 'passwordSignIn.title',
  sms: 'smsSignIn.title',
  passkey: 'signInFlow.factorKind.webauthn',
  google: 'social.provider.google',
  github: 'social.provider.github',
  wechat: 'social.provider.wechat',
  dingtalk: 'social.provider.dingtalk',
  feishu: 'social.provider.feishu',
}

const METHOD_MODE_LABEL_KEYS: Readonly<Record<string, string>> = {
  'wechat.mp': 'social.provider.wechatOfficialAccount',
  'wechat.web': 'social.provider.wechatWeb',
}

const FIELD_LABEL_KEYS: Readonly<Record<string, string>> = {
  identifier: 'passwordSignIn.identifierLabel',
  password: 'passwordSignIn.passwordLabel',
  new_password: 'passwordSignIn.newPasswordLabel',
  phone: 'smsSignIn.phoneLabel',
  code: 'smsSignIn.codeLabel',
  username: 'signInFlow.usernameLabel',
  display_name: 'register.displayNameLabel',
  email: 'register.emailLabel',
}

const FIELD_HELP_KEYS: Readonly<Record<string, string>> = {
  username: 'signInFlow.usernameHelper',
  display_name: 'signInFlow.signupDisplayNameHelp',
}

export type Translate = (key: string) => string

export function methodLabel(
  method: { readonly name: string; readonly label?: string },
  t: Translate,
): string {
  const modeKey = METHOD_MODE_LABEL_KEYS[method.name]
  if (modeKey !== undefined) {
    return t(modeKey)
  }
  if (method.label !== undefined && method.label !== '') {
    return method.label
  }
  const key = METHOD_LABEL_KEYS[method.name]
  return key === undefined ? method.name : t(key)
}

export function fieldLabel(field: AuthnField, t: Translate): string {
  const key = FIELD_LABEL_KEYS[field.name]
  return key === undefined ? field.name : t(key)
}

export function fieldHelp(field: AuthnField, t: Translate): string | null {
  const key = FIELD_HELP_KEYS[field.name]
  return key === undefined ? null : t(key)
}
