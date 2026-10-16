/**
 * The sign-in family's label vocabulary.
 *
 * A method's display name comes from the discovery document, not the
 * component: the descriptor's brand label where the method carries one
 * (Google, WeChat -- a provider's own name, not translated), otherwise
 * the bundle's wording for a name the family knows, otherwise the
 * method's own name (a server-side method the family has no wording for
 * shows its name rather than a translated guess). A field's label follows
 * the same rule: a name the family knows renders its translated wording,
 * an unknown one renders the name verbatim.
 */

import type { AuthnField } from '@speed/auth-core'

/** The bundle keys of the method names the family has wording for. */
const METHOD_LABEL_KEYS: Readonly<Record<string, string>> = {
  password: 'passwordSignIn.title',
  sms: 'smsSignIn.title',
  passkey: 'signInFlow.factorKind.webauthn',
  google: 'social.provider.google',
  github: 'social.provider.github',
  wechat: 'social.provider.wechat',
  'wechat.web': 'social.provider.wechat',
  'wechat.mp': 'social.provider.wechat',
  dingtalk: 'social.provider.dingtalk',
  feishu: 'social.provider.feishu',
}

/** The bundle keys of the field names the family knows. */
const FIELD_LABEL_KEYS: Readonly<Record<string, string>> = {
  identifier: 'passwordSignIn.identifierLabel',
  password: 'passwordSignIn.passwordLabel',
  new_password: 'passwordSignIn.newPasswordLabel',
  phone: 'smsSignIn.phoneLabel',
  code: 'smsSignIn.codeLabel',
}

/** A translator bound to the auth-ui namespace. */
export type Translate = (key: string) => string

/** A method's display name (see the module header). */
export function methodLabel(
  method: { readonly name: string; readonly label?: string },
  t: Translate,
): string {
  if (method.label !== undefined && method.label !== '') {
    return method.label
  }
  const key = METHOD_LABEL_KEYS[method.name]
  return key === undefined ? method.name : t(key)
}

/** A field's display label (see the module header). */
export function fieldLabel(field: AuthnField, t: Translate): string {
  const key = FIELD_LABEL_KEYS[field.name]
  return key === undefined ? field.name : t(key)
}
