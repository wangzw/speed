// A verification code is read from the `verification_code` field, never `code`: the logging layer masks `code` as a sensitive key name.

export type VerificationKind = 'sms' | 'mail'

export interface VerificationCode {
  readonly code: string
  readonly address: string
  readonly line: string
  readonly index: number
}

const CODE_MESSAGE: Record<VerificationKind, string> = {
  sms: 'log sms: a verification code was sent',
  mail: 'log mail: a message was sent',
}

// The kinds are told apart by the template and language the line names, never by the message text alone.
const SMS_CARRIER_MESSAGES: ReadonlySet<string> = new Set([
  CODE_MESSAGE.sms,
  'log sms: a message was sent',
])

// The mail line carries the rendered subject, not a template key or a language.
const MAIL_CARRIER_MESSAGE = 'log mail: a message was sent'

const OUTBOUND_SENT_MESSAGE =
  'notification.delivery: an external message was sent'

const OUTBOUND_FAILED_MESSAGE =
  'notification.delivery: an external message was not sent'

const READY_MESSAGE = 'ready'

const CODE_SHAPE = /^\d{6}$/

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
      return [marker ?? '', start + 2]
  }
}

// Parse one logfmt line: a quoted value keeps its spaces, escapes and newlines, and a token with no `=` is skipped.
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

function names(address: string, want: string): boolean {
  const needle = want.trim().toLowerCase()
  return address
    .split(',')
    .map((part) => part.trim().toLowerCase())
    .some((part) => part === needle)
}

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

export class ProcessLog {
  private readonly captured: string[] = []
  private readonly listeners = new Set<() => void>()

  append(line: string): void {
    this.captured.push(line)
    for (const listener of [...this.listeners]) listener()
  }

  get lines(): readonly string[] {
    return this.captured
  }

  get text(): string {
    return this.captured.join('\n')
  }

  mark(): number {
    return this.captured.length
  }

  tail(count = 40): string {
    return this.captured.slice(Math.max(0, this.captured.length - count)).join('\n')
  }

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

// `after` is a log cursor, so a bind retry cannot match a record written by the failed attempt.
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

// Answers the latest code for this kind and destination at or after `after`.
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

export interface CarrierMessage {
  readonly to: string
  readonly template: string
  readonly locale: string
  readonly subject: string
  readonly text: string
  readonly line: string
  readonly index: number
}

export interface OutboundRecord {
  readonly channel: string
  readonly typeKey: string
  readonly outcome: string
  readonly providerID: string
  readonly durationMS: number
  readonly line: string
  readonly index: number
}

interface CarrierMessageFilter {
  readonly to?: string
  readonly template?: string
  readonly locale?: string
  readonly subject?: string
  readonly textIncludes?: string
}

function carrierMessageOnLine(
  line: string,
  options: CarrierMessageFilter,
): CarrierMessage | undefined {
  const fields = parseLogfmt(line)
  const msg = fields.msg ?? ''
  const to = fields.to ?? ''

  if (SMS_CARRIER_MESSAGES.has(msg)) {
    const template = fields.template ?? ''
    const locale = fields.locale ?? ''
    if (template === '' || locale === '') return undefined
    if (options.to !== undefined && to !== options.to) return undefined
    if (options.template !== undefined && template !== options.template) {
      return undefined
    }
    if (options.locale !== undefined && locale !== options.locale) {
      return undefined
    }
    if (options.subject !== undefined || options.textIncludes !== undefined) {
      return undefined
    }
    return { to, template, locale, subject: '', text: '', line, index: -1 }
  }

  if (msg === MAIL_CARRIER_MESSAGE) {
    const subject = fields.subject ?? ''
    const text = fields.text ?? ''
    if (subject === '') return undefined
    if (options.to !== undefined && to !== options.to) return undefined
    if (options.template !== undefined || options.locale !== undefined) {
      return undefined
    }
    if (options.subject !== undefined && subject !== options.subject) {
      return undefined
    }
    if (
      options.textIncludes !== undefined &&
      !text.includes(options.textIncludes)
    ) {
      return undefined
    }
    return { to, template: '', locale: '', subject, text, line, index: -1 }
  }

  return undefined
}

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

export async function waitForCarrierMessage(
  log: ProcessLog,
  options: CarrierMessageFilter & {
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
        'the log carrier line' +
        (options.template === undefined ? '' : ` for ${options.template}`) +
        (options.subject === undefined ? '' : ` headed ${options.subject}`),
    },
  )
  const message = carrierMessageOnLine(found.line, options)
  if (message === undefined) {
    throw new Error(`the line that answered the wait is not a carrier line: ${found.line}`)
  }
  return { ...message, index: found.index }
}

export function countCarrierMessages(
  log: ProcessLog,
  options: CarrierMessageFilter & {
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

// A locally refused send writes no record; count records instead of waiting when a refusal is expected.
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
