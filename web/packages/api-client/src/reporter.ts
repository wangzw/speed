/**
 * A structured diagnostic sink with error and warn channels; messages are
 * constant English plus snake_case attributes. The client itself only calls
 * warn.
 */
export interface Reporter {
  /** Report a failure: constant message, snake_case attributes. */
  error(message: string, attrs?: Readonly<Record<string, unknown>>): void
  /** Report something worth noting: constant message, snake_case attrs. */
  warn(message: string, attrs?: Readonly<Record<string, unknown>>): void
}

/** The console-backed Reporter used when ClientOptions.reporter is omitted. */
export function createConsoleReporter(): Reporter {
  return {
    error(message: string, attrs?: Readonly<Record<string, unknown>>): void {
      console.error(message, attrs)
    },
    warn(message: string, attrs?: Readonly<Record<string, unknown>>): void {
      console.warn(message, attrs)
    },
  }
}
