import type { Reporter } from '../src/index'

export interface MemoryReport {
  readonly message: string
  readonly attrs: Readonly<Record<string, unknown>> | undefined
}

export interface MemoryReporter {
  /** Inject as ClientOptions.reporter. */
  readonly reporter: Reporter
  /** Every error() call, in order. */
  readonly errors: readonly MemoryReport[]
  /** Every warn() call, in order. */
  readonly warns: readonly MemoryReport[]
}

export function createMemoryReporter(): MemoryReporter {
  const errors: MemoryReport[] = []
  const warns: MemoryReport[] = []
  return {
    reporter: {
      error(message: string, attrs?: Readonly<Record<string, unknown>>): void {
        errors.push({ message, attrs })
      },
      warn(message: string, attrs?: Readonly<Record<string, unknown>>): void {
        warns.push({ message, attrs })
      },
    },
    errors,
    warns,
  }
}
