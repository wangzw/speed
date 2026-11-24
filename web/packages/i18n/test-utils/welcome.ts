import {
  createI18n,
  registerNamespace,
  SPEED_LOCALE_STORAGE_KEY,
  type I18nInstance,
  type MissingKeyDetails,
  type ResourceBundle,
} from '../src/index'
import { MemoryStorage } from './memory-storage'
import welcomeEn from './locales/welcome/en-US.json'
import welcomeZh from './locales/welcome/zh-CN.json'

export const welcomeResources: Readonly<Record<string, ResourceBundle>> = {
  'zh-CN': welcomeZh as unknown as ResourceBundle,
  'en-US': welcomeEn as unknown as ResourceBundle,
}

export function registerWelcome(instance: I18nInstance): void {
  registerNamespace(instance, 'welcome', welcomeResources)
}

export interface CreateTestI18nOptions {
  readonly storedLanguage?: string | null
  readonly navigatorLanguages?: readonly string[]
  readonly profileLanguage?: string | null
  readonly onMissingKey?: (details: MissingKeyDetails) => void
}

export function createTestI18n(
  options: CreateTestI18nOptions = {},
): { instance: I18nInstance; storage: MemoryStorage } {
  const storage = new MemoryStorage()
  if (options.storedLanguage !== null && options.storedLanguage !== undefined) {
    storage.setItem(SPEED_LOCALE_STORAGE_KEY, options.storedLanguage)
  }
  const instance = createI18n({
    storage,
    navigatorLanguages: options.navigatorLanguages ?? [],
    profileLanguage: options.profileLanguage ?? null,
    onMissingKey: options.onMissingKey,
  })
  registerWelcome(instance)
  return { instance, storage }
}
