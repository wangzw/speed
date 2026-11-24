import { createSocket, type RemoteInfo, type Socket } from 'node:dgram'

const TYPE_TXT = 16
const CLASS_IN = 1

const RCODE_NOERROR = 0
const RCODE_FORMERR = 1
const RCODE_NXDOMAIN = 3

const ANSWER_TTL_SECONDS = 60

const MAX_TXT_CHARS = 255

interface Question {
  readonly name: string
  readonly type: number
  readonly class: number
  readonly end: number
}

export interface DNSStub {
  readonly address: string
  readonly recordName: string
  setTXT(records: readonly string[]): void
  stop(): Promise<void>
}

export interface DNSStubInput {
  readonly recordName: string
}

function normalizeName(name: string): string {
  return name.toLowerCase().replace(/\.+$/, '')
}

function readQuestion(message: Buffer): Question | undefined {
  if (message.length < 12) return undefined
  let offset = 12
  const labels: string[] = []
  for (;;) {
    const length = message[offset]
    if (length === undefined) return undefined
    offset += 1
    if (length === 0) break
    if ((length & 0xc0) !== 0) return undefined
    if (length > 63 || offset + length > message.length) return undefined
    labels.push(message.subarray(offset, offset + length).toString('ascii'))
    offset += length
  }
  if (offset + 4 > message.length) return undefined
  return {
    name: normalizeName(labels.join('.')),
    type: message.readUInt16BE(offset),
    class: message.readUInt16BE(offset + 2),
    end: offset + 4,
  }
}

// A value longer than 255 bytes is carried as several character-strings of one record; a resolver concatenates them back into the value.
function txtStrings(value: string): Buffer[] {
  const bytes = Buffer.from(value, 'utf8')
  if (bytes.length === 0) return [Buffer.from([0])]
  const strings: Buffer[] = []
  for (let offset = 0; offset < bytes.length; offset += MAX_TXT_CHARS) {
    const slice = bytes.subarray(offset, offset + MAX_TXT_CHARS)
    const prefixed = Buffer.alloc(slice.length + 1)
    prefixed[0] = slice.length
    slice.copy(prefixed, 1)
    strings.push(prefixed)
  }
  return strings
}

// The record name is the question's, by compression pointer 0xc00c.
function txtRecord(value: string): Buffer {
  const rdata = Buffer.concat(txtStrings(value))
  const record = Buffer.alloc(12)
  record.writeUInt16BE(0xc00c, 0)
  record.writeUInt16BE(TYPE_TXT, 2)
  record.writeUInt16BE(CLASS_IN, 4)
  record.writeUInt32BE(ANSWER_TTL_SECONDS, 6)
  record.writeUInt16BE(rdata.length, 10)
  return Buffer.concat([record, rdata])
}

function encodeResponse(
  query: Buffer,
  question: Question | undefined,
  rcode: number,
  records: readonly string[],
): Buffer {
  const header = Buffer.alloc(12)
  if (query.length >= 2) query.copy(header, 0, 0, 2)
  const requestFlags = query.length >= 4 ? query.readUInt16BE(2) : 0
  // QR, the query's opcode and RD bit, AA (the zone's own answer), and the rcode.
  const flags =
    0x8000 | (requestFlags & 0x7800) | 0x0400 | (requestFlags & 0x0100) | rcode
  header.writeUInt16BE(flags, 2)
  header.writeUInt16BE(question === undefined ? 0 : 1, 4)
  header.writeUInt16BE(records.length, 6)
  header.writeUInt16BE(0, 8)
  header.writeUInt16BE(0, 10)
  const parts: Buffer[] = [header]
  if (question !== undefined) parts.push(query.subarray(12, question.end))
  for (const value of records) parts.push(txtRecord(value))
  return Buffer.concat(parts)
}

export async function startDNSStub(input: DNSStubInput): Promise<DNSStub> {
  const recordName = normalizeName(input.recordName)
  if (recordName === '') {
    throw new Error('the DNS stub needs a record name to answer for')
  }

  let records: readonly string[] = []
  let failure: Error | undefined
  let stopped = false
  const socket: Socket = createSocket('udp4')

  const onMessage = (message: Buffer, remote: RemoteInfo): void => {
    const questions = message.length >= 12 ? message.readUInt16BE(4) : 0
    const question = questions === 1 ? readQuestion(message) : undefined
    let reply: Buffer
    if (question === undefined) {
      reply = encodeResponse(message, undefined, RCODE_FORMERR, [])
    } else if (
      question.name !== recordName ||
      question.type !== TYPE_TXT ||
      question.class !== CLASS_IN ||
      records.length === 0
    ) {
      reply = encodeResponse(message, question, RCODE_NXDOMAIN, [])
    } else {
      reply = encodeResponse(message, question, RCODE_NOERROR, records)
    }
    socket.send(reply, remote.port, remote.address, (error) => {
      if (error !== null && !stopped && failure === undefined) failure = error
    })
  }

  await new Promise<void>((resolve, reject) => {
    const onError = (error: Error): void => {
      socket.off('listening', onListening)
      reject(error)
    }
    const onListening = (): void => {
      socket.off('error', onError)
      resolve()
    }
    socket.once('error', onError)
    socket.once('listening', onListening)
    socket.bind(0, '127.0.0.1')
  })

  const bound = socket.address()
  if (bound === null || typeof bound === 'string') {
    socket.close()
    throw new Error('the DNS stub answered without a loopback address')
  }
  socket.on('message', onMessage)
  socket.on('error', (error: Error) => {
    if (!stopped && failure === undefined) failure = error
  })

  return {
    address: `127.0.0.1:${bound.port}`,
    recordName,
    setTXT(next: readonly string[]): void {
      records = [...next]
    },
    async stop(): Promise<void> {
      if (stopped) return
      stopped = true
      socket.off('message', onMessage)
      await new Promise<void>((resolve) => {
        socket.close(() => resolve())
      })
      if (failure !== undefined) throw failure
    },
  }
}
