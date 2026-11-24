import type { StorageLike } from '../src/storage'

export class MemoryStorage implements StorageLike {
  private readonly values = new Map<string, string>()

  getItem(key: string): string | null {
    return this.values.get(key) ?? null
  }

  setItem(key: string, value: string): void {
    this.values.set(key, value)
  }

  entries(): ReadonlyMap<string, string> {
    return this.values
  }
}
