/**
 * A UDP DNS stub for the enterprise journey's domain-ownership proof.
 *
 * The product verifies a claimed email domain by looking up the TXT record at
 * `_speed-authn.<domain>` through the resolver the deployment names. The review
 * configuration leaves that lookup on the system resolver, which cannot answer
 * for a test domain. A spec that drives the enterprise journey asks the server
 * fixture for this stub and names its address as `authn.sso.dns-resolver`.
 *
 * The stub is deliberately not a resolver. It reads one question out of each
 * query and answers only when that question is a TXT query for its one record
 * name; every other name, type and class answers NXDOMAIN, and nothing is ever
 * forwarded. That bound is what keeps the journey from depending on, or being
 * able to reach, any name outside the test domain.
 *
 * The record's value is published by the spec after the product renders the
 * one-time verification token: the operator's own flow is to copy the token the
 * page shows into the record. `setTXT` replaces the answer set; an empty set
 * answers NXDOMAIN, which is the state before the operator publishes anything.
 *
 * Only fixtures may assemble the environment. A spec may call the returned
 * handle to publish the record a person would publish, and may never answer a
 * query itself.
 */
import { createSocket, type RemoteInfo, type Socket } from 'node:dgram'

/** The record type and class the stub answers. */
const TYPE_TXT = 16
const CLASS_IN = 1

/** The response codes the stub uses. */
const RCODE_NOERROR = 0
const RCODE_FORMERR = 1
const RCODE_NXDOMAIN = 3

/** A TTL that lasts a run without asking anyone to cache it. */
const ANSWER_TTL_SECONDS = 60

/** The largest character-string a TXT record may carry. */
const MAX_TXT_CHARS = 255

/** One question read out of a query packet. */
interface Question {
  /** The lowercased name, without its trailing dot. */
  readonly name: string
  readonly type: number
  readonly class: number
  /** The offset just past the question, where the answer section starts. */
  readonly end: number
}

/** A running DNS stub. */
export interface DNSStub {
  /** The resolver address, `127.0.0.1:<port>`. */
  readonly address: string
  /** The one name the stub answers TXT queries for. */
  readonly recordName: string
  /** Replace the TXT record set; an empty set answers NXDOMAIN. */
  setTXT(records: readonly string[]): void
  stop(): Promise<void>
}

export interface DNSStubInput {
  /** The one record name the stub answers; every other name is NXDOMAIN. */
  readonly recordName: string
}

/** Lowercase a name and drop its trailing dot, for exact comparison. */
function normalizeName(name: string): string {
  return name.toLowerCase().replace(/\.+$/, '')
}

/**
 * Read the single question of a query packet. A packet with a header shorter
 * than the header, or a name that uses a compression pointer (queries do not),
 * reads as unreadable.
 */
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

/**
 * Split one TXT value into length-prefixed character-strings. A value longer
 * than one character-string is carried as the several strings of one record,
 * which a resolver concatenates back into the value.
 */
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

/** One TXT resource record whose name is the question's, by compression. */
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

/** Build one response, echoing the question when there is one. */
function encodeResponse(
  query: Buffer,
  question: Question | undefined,
  rcode: number,
  records: readonly string[],
): Buffer {
  const header = Buffer.alloc(12)
  if (query.length >= 2) query.copy(header, 0, 0, 2)
  const requestFlags = query.length >= 4 ? query.readUInt16BE(2) : 0
  // QR, the query's opcode and RD bit, AA because this is the zone's own
  // answer, and the response code.
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

/**
 * Start a stub that answers TXT queries for `input.recordName` and nothing
 * else, bound to a free loopback port. `address` is the value a review
 * configuration names as its DNS resolver.
 */
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
