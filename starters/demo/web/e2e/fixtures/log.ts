/**
 * The review process's standard output, and the one thing the tests read out
 * of it: a verification code.
 *
 * The review assembly writes its logfmt records to standard output (pkg/log's
 * bootstrap chain). The verification code is the single test-side read the
 * honesty rules allow: the carriers reach no vendor or relay, so the process log
 * is the only place a person running the review would see the code. Nothing
 * else in this directory may interpret log lines; the server fixture hands its
 * captured lines to `ProcessLog`, and the helpers ask this module for a code by
 * kind and destination address.
 *
 * The code is read from the `verification_code` field, never from `code`: the
 * gateway registers `code` as a sensitive key name and the logging layer masks
 * its value, so a line logged under that name would carry no usable code.
 *
 * The parser is a small logfmt reader rather than a split on spaces because
 * the mail record carries quoted bodies: `text` and `html` may contain spaces,
 * quotes and newlines, and the code is written after them.
 */

/** The two carriers the review assembly writes code records for. */
export type VerificationKind = 'sms' | 'mail'

/** A verification code read from the process log. */
export interface VerificationCode {
  readonly code: string
  readonly address: string
  readonly line: string
  readonly index: number
}

/** The `msg` each carrier writes when it has sent a code. */
const CODE_MESSAGE: Record<VerificationKind, string> = {
  sms: 'review sms: a verification code was sent',
  mail: 'review mail: a verification message was sent',
}

/** The startup record the server fixture waits for. */
const READY_MESSAGE = 'ready'

const CODE_SHAPE = /^\d{6}$/

/**
 * Decode one backslash escape from strconv.Quote's repertoire, answering the
 * text it stands for and the index just past it.
 */
function unescape(text: string, start: number): [string, number] {
  const marker = text[start + 1]
  switch (marker) {
    case 'n':
      return ['\n', start + 2]
    case 'r':
      return ['\r', start + 2]
    case 't':
      return ['\t', start + 2]
    case 'a':
      return ['\u0007', start + 2]
    case 'b':
      return ['\b', start + 2]
    case 'f':
      return ['\f', start + 2]
    case 'v':
      return ['\v', start + 2]
    case 'x': {
      const hex = text.slice(start + 2, start + 4)
      return [String.fromCharCode(Number.parseInt(hex, 16)), start + 4]
    }
    case 'u': {
      const hex = text.slice(start + 2, start + 6)
      return [String.fromCharCode(Number.parseInt(hex, 16)), start + 6]
    }
    case 'U': {
      const hex = text.slice(start + 2, start + 10)
      return [String.fromCodePoint(Number.parseInt(hex, 16)), start + 10]
    }
    default:
      // Covers the escaped quote and backslash, and any marker this reader
      // does not know: the character itself is the best answer available.
      return [marker ?? '', start + 2]
  }
}

/**
 * Parse one logfmt line into its fields. A quoted value keeps its spaces,
 * escapes and newlines; an unquoted value ends at the next space. A token with
 * no `=` is skipped rather than treated as a field.
 */
export function parseLogfmt(line: string): Record<string, string> {
  const fields: Record<string, string> = {}
  let index = 0
  while (index < line.length) {
    while (index < line.length && line[index] === ' ') index += 1
    if (index >= line.length) break
    const keyStart = index
    while (index < line.length && line[index] !== '=' && line[index] !== ' ') {
      index += 1
    }
    if (index >= line.length || line[index] !== '=') {
      while (index < line.length && line[index] !== ' ') index += 1
      continue
    }
    const key = line.slice(keyStart, index)
    index += 1
    let value = ''
    if (line[index] === '"') {
      index += 1
      while (index < line.length) {
        const char = line[index]
        if (char === '\\') {
          const [decoded, next] = unescape(line, index)
          value += decoded
          index = next
          continue
        }
        if (char === '"') {
          index += 1
          break
        }
        value += char
        index += 1
      }
    } else {
      const valueStart = index
      while (index < line.length && line[index] !== ' ') index += 1
      value = line.slice(valueStart, index)
    }
    if (key !== '') fields[key] = value
  }
  return fields
}

/** Whether a `to` value names the address, among a comma-joined recipient list. */
function names(address: string, want: string): boolean {
  const needle = want.trim().toLowerCase()
  return address
    .split(',')
    .map((part) => part.trim().toLowerCase())
    .some((part) => part === needle)
}

/** The code a line carries for this kind and address, if it carries one. */
function codeOnLine(
  line: string,
  kind: VerificationKind,
  address: string,
): string | undefined {
  const fields = parseLogfmt(line)
  if (fields.msg !== CODE_MESSAGE[kind]) return undefined
  const to = fields.to
  if (to === undefined || !names(to, address)) return undefined
  const code = fields.verification_code
  if (code === undefined || !CODE_SHAPE.test(code)) return undefined
  return code
}

/**
 * A line buffer the server fixture fills from the child's standard output.
 * Waiting is append-driven: a waiter is woken when a line arrives, so a code
 * that is written after the call is answered as soon as it lands.
 */
export class ProcessLog {
  private readonly captured: string[] = []
  private readonly listeners = new Set<() => void>()

  /** Append one line and wake every waiter. */
  append(line: string): void {
    this.captured.push(line)
    for (const listener of [...this.listeners]) listener()
  }

  /** Every line captured so far. */
  get lines(): readonly string[] {
    return this.captured
  }

  /** The whole capture, for a failure attachment. */
  get text(): string {
    return this.captured.join('\n')
  }

  /** The number of lines captured so far: a cursor for `after`. */
  mark(): number {
    return this.captured.length
  }

  /** The last `count` lines, for an error message. */
  tail(count = 40): string {
    return this.captured.slice(Math.max(0, this.captured.length - count)).join('\n')
  }

  /**
   * Wait until a line at or after `after` satisfies the predicate. The answer
   * carries the line and its index so a caller can continue from it.
   */
  async waitFor(
    predicate: (line: string, index: number) => boolean,
    options: {
      readonly timeoutMs: number
      readonly what: string
      readonly after?: number
    },
  ): Promise<{ line: string; index: number }> {
    const start = Math.max(0, options.after ?? 0)
    const deadline = Date.now() + options.timeoutMs
    for (;;) {
      for (let index = start; index < this.captured.length; index += 1) {
        const line = this.captured[index]
        if (line !== undefined && predicate(line, index)) {
          return { line, index }
        }
      }
      const remaining = deadline - Date.now()
      if (remaining <= 0) {
        throw new Error(
          `timed out after ${options.timeoutMs}ms waiting for ${options.what}` +
            `\n--- process log (last lines) ---\n${this.tail()}`,
        )
      }
      await this.waitForChange(remaining)
    }
  }

  /** Resolve when a line arrives, or when the remaining time runs out. */
  private waitForChange(remainingMs: number): Promise<void> {
    return new Promise((resolve) => {
      const wake = (): void => {
        clearTimeout(timer)
        this.listeners.delete(wake)
        resolve()
      }
      const timer = setTimeout(wake, remainingMs)
      this.listeners.add(wake)
    })
  }
}

/**
 * Wait for the startup record, which is written after every module started.
 * `after` is a log cursor: a bind retry passes the position taken before its
 * own child started, so a record from the failed attempt cannot answer it.
 */
export async function waitForReady(
  log: ProcessLog,
  timeoutMs: number,
  after = 0,
): Promise<void> {
  await log.waitFor((line) => parseLogfmt(line).msg === READY_MESSAGE, {
    timeoutMs,
    what: 'the msg=ready startup record',
    after,
  })
}

/**
 * Wait for a verification code for this kind and destination, and answer the
 * latest one at or after `after`. The `after` cursor is what a caller uses when
 * the same address has received a code before and only the new one may be
 * accepted.
 */
export async function waitForVerificationCode(
  log: ProcessLog,
  kind: VerificationKind,
  address: string,
  options: {
    readonly timeoutMs?: number
    readonly after?: number
  } = {},
): Promise<VerificationCode> {
  const after = options.after ?? 0
  const timeoutMs = options.timeoutMs ?? 30_000
  const found = await log.waitFor(
    (line) => codeOnLine(line, kind, address) !== undefined,
    {
      timeoutMs,
      after,
      what: `a ${kind} verification code for ${address}`,
    },
  )
  // Another send may have landed while this one was being observed; the
  // latest line is the one that belongs to the most recent request.
  let latest: VerificationCode = {
    code: codeOnLine(found.line, kind, address) ?? '',
    address,
    line: found.line,
    index: found.index,
  }
  for (let index = found.index + 1; index < log.lines.length; index += 1) {
    const line = log.lines[index]
    if (line === undefined) continue
    const code = codeOnLine(line, kind, address)
    if (code !== undefined) latest = { code, address, line, index }
  }
  return latest
}
