/**
 * The review process's standard output, and the three things the tests read out
 * of it: a verification code, the line that records a message accepted by the
 * review SMS carrier, and the line the delivery pipeline writes for an external
 * send.
 *
 * The review assembly writes its logfmt records to standard output (pkg/log's
 * bootstrap chain). These are the test-side reads the honesty rules allow: the
 * carriers reach no vendor or relay, so the process log is the only place a
 * person running the review would see a code or a notice leave. Nothing else in
 * this directory may interpret log lines; the server fixture hands its captured
 * lines to `ProcessLog`, and the callers ask this module for a code by kind and
 * destination address, a carrier message by destination, template and language,
 * or an outbound record by channel, type and outcome.
 *
 * A verification code is read from the `verification_code` field, never from
 * `code`: the gateway registers `code` as a sensitive key name and the logging
 * layer masks its value, so a line logged under that name would carry no usable
 * code. A notification message carries no code and writes no such field.
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

/**
 * The `msg` the review SMS carrier writes for a resolved message. The carrier
 * sends two kinds: a verification code, which keeps the line above, and a
 * notification, whose line does not claim to carry a code. A caller tells the
 * kinds apart by the template and the language the line names, never by the
 * message text alone.
 */
const SMS_CARRIER_MESSAGES: ReadonlySet<string> = new Set([
  CODE_MESSAGE.sms,
  'review sms: a message was sent',
])

/** The `msg` notification.delivery writes for an external send it accepted. */
const OUTBOUND_SENT_MESSAGE =
  'notification.delivery: an external message was sent'

/** The `msg` notification.delivery writes for an external send it did not. */
const OUTBOUND_FAILED_MESSAGE =
  'notification.delivery: an external message was not sent'

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

/** One carrier line: the message the carrier wrote when it sent, and where. */
export interface CarrierMessage {
  /** The destination the carrier was handed. */
  readonly to: string
  /** The logical template the message was sent under. */
  readonly template: string
  /** The language the message was resolved in. */
  readonly locale: string
  /** The whole line, for a failure attachment. */
  readonly line: string
  /** The line's position, so a caller can continue from it. */
  readonly index: number
}

/** One external-send record the delivery pipeline wrote. */
export interface OutboundRecord {
  /** The channel the attempt was for: in_app, email or sms. */
  readonly channel: string
  /** The notification type the dispatch carried. */
  readonly typeKey: string
  /** The closed outcome the pipeline classified the attempt as. */
  readonly outcome: string
  /** The carrier's own receipt identifier, empty for a refusal. */
  readonly providerID: string
  /** The call's duration in milliseconds. */
  readonly durationMS: number
  /** The whole line, for a failure attachment. */
  readonly line: string
  /** The line's position, so a caller can continue from it. */
  readonly index: number
}

/** A carrier line this kind of message wrote, if the line is one. */
function carrierMessageOnLine(
  line: string,
  options: { readonly to?: string; readonly template?: string; readonly locale?: string },
): CarrierMessage | undefined {
  const fields = parseLogfmt(line)
  if (!SMS_CARRIER_MESSAGES.has(fields.msg ?? '')) return undefined
  const to = fields.to ?? ''
  const template = fields.template ?? ''
  const locale = fields.locale ?? ''
  // A carrier line is identified by what the send resolved to: the template and
  // the language. Both message kinds carry them, so a caller can await either
  // without one being mistaken for the other, and a line missing them is not a
  // message this reader can attribute.
  if (template === '' || locale === '') return undefined
  if (options.to !== undefined && to !== options.to) return undefined
  if (options.template !== undefined && template !== options.template) {
    return undefined
  }
  if (options.locale !== undefined && locale !== options.locale) return undefined
  return { to, template, locale, line, index: -1 }
}

/** An external-send line the pipeline wrote, if the line is one. */
function outboundRecordOnLine(
  line: string,
  options: {
    readonly channel?: string
    readonly typeKey?: string
    readonly outcome?: string
  },
): OutboundRecord | undefined {
  const fields = parseLogfmt(line)
  const msg = fields.msg
  if (msg !== OUTBOUND_SENT_MESSAGE && msg !== OUTBOUND_FAILED_MESSAGE) {
    return undefined
  }
  const channel = fields.channel ?? ''
  const typeKey = fields.type_key ?? ''
  const outcome = fields.outcome ?? ''
  if (options.channel !== undefined && channel !== options.channel) {
    return undefined
  }
  if (options.typeKey !== undefined && typeKey !== options.typeKey) {
    return undefined
  }
  if (options.outcome !== undefined && outcome !== options.outcome) {
    return undefined
  }
  const duration = Number(fields.duration_ms ?? '')
  return {
    channel,
    typeKey,
    outcome,
    providerID: fields.provider_id ?? '',
    durationMS: Number.isFinite(duration) ? duration : 0,
    line,
    index: -1,
  }
}

/**
 * Wait for the carrier line that records one SMS being handed to it. The line
 * carries the destination, the logical template and the language it was
 * resolved in; the review carrier writes it instead of reaching a vendor, so it
 * is the only evidence a browser tier can read that the message left.
 */
export async function waitForCarrierMessage(
  log: ProcessLog,
  options: {
    readonly to?: string
    readonly template?: string
    readonly locale?: string
    readonly timeoutMs?: number
    readonly after?: number
  },
): Promise<CarrierMessage> {
  const found = await log.waitFor(
    (line) => carrierMessageOnLine(line, options) !== undefined,
    {
      timeoutMs: options.timeoutMs ?? 30_000,
      after: options.after ?? 0,
      what:
        'the review SMS carrier line' +
        (options.template === undefined ? '' : ` for ${options.template}`),
    },
  )
  const message = carrierMessageOnLine(found.line, options)
  if (message === undefined) {
    throw new Error(`the line that answered the wait is not a carrier line: ${found.line}`)
  }
  return { ...message, index: found.index }
}

/** Count the carrier lines a filter names, at or after `after`. */
export function countCarrierMessages(
  log: ProcessLog,
  options: {
    readonly to?: string
    readonly template?: string
    readonly locale?: string
    readonly after?: number
  },
): number {
  const after = Math.max(0, options.after ?? 0)
  let count = 0
  for (let index = after; index < log.lines.length; index += 1) {
    const line = log.lines[index]
    if (line !== undefined && carrierMessageOnLine(line, options) !== undefined) {
      count += 1
    }
  }
  return count
}

/**
 * Wait for one external-send record the delivery pipeline wrote. A channel that
 * did not reach a carrier — a local refusal before the send — writes none, which
 * is what a caller counts instead of waiting when the send is expected to be
 * refused.
 */
export async function waitForOutboundRecord(
  log: ProcessLog,
  options: {
    readonly channel?: string
    readonly typeKey?: string
    readonly outcome?: string
    readonly timeoutMs?: number
    readonly after?: number
  },
): Promise<OutboundRecord> {
  const found = await log.waitFor(
    (line) => outboundRecordOnLine(line, options) !== undefined,
    {
      timeoutMs: options.timeoutMs ?? 30_000,
      after: options.after ?? 0,
      what:
        'an external-send record' +
        (options.channel === undefined ? '' : ` on ${options.channel}`),
    },
  )
  const record = outboundRecordOnLine(found.line, options)
  if (record === undefined) {
    throw new Error(`the line that answered the wait is not an outbound record: ${found.line}`)
  }
  return { ...record, index: found.index }
}

/** Count the external-send records a filter names, at or after `after`. */
export function countOutboundRecords(
  log: ProcessLog,
  options: {
    readonly channel?: string
    readonly typeKey?: string
    readonly outcome?: string
    readonly after?: number
  },
): number {
  const after = Math.max(0, options.after ?? 0)
  let count = 0
  for (let index = after; index < log.lines.length; index += 1) {
    const line = log.lines[index]
    if (line !== undefined && outboundRecordOnLine(line, options) !== undefined) {
      count += 1
    }
  }
  return count
}
