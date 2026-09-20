import type { PhoneEvent } from './phone'

export async function readPhoneEvents(
  stream: ReadableStream<Uint8Array>,
  onEvent: (event: PhoneEvent) => void,
  idleTimeoutMs = 45_000
) {
  const reader = stream.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  try {
    for (;;) {
      let timer: ReturnType<typeof setTimeout> | undefined
      const read = reader.read()
      const timeout = new Promise<never>((_, reject) => {
        timer = setTimeout(() => reject(new Error('电话事件流心跳超时，正在重连')), idleTimeoutMs)
      })
      const { value, done } = await Promise.race([read, timeout]).finally(() => clearTimeout(timer))
      buffer = (buffer + decoder.decode(value, { stream: !done })).replaceAll('\r\n', '\n')
      buffer = consumeEventBlocks(buffer, onEvent)
      if (done) {
        if (buffer.trim()) parsePhoneEventBlock(buffer, onEvent)
        return
      }
    }
  } finally {
    await reader.cancel().catch(() => {})
    reader.releaseLock()
  }
}

export function consumeEventBlocks(buffer: string, onEvent: (event: PhoneEvent) => void) {
  let remaining = buffer
  let boundary = remaining.indexOf('\n\n')
  while (boundary >= 0) {
    parsePhoneEventBlock(remaining.slice(0, boundary), onEvent)
    remaining = remaining.slice(boundary + 2)
    boundary = remaining.indexOf('\n\n')
  }
  return remaining
}

export function parsePhoneEventBlock(block: string, onEvent: (event: PhoneEvent) => void) {
  const data = block
    .split('\n')
    .filter((line) => line.startsWith('data:'))
    .map((line) => line.slice(5).trimStart())
    .join('\n')
  if (data) onEvent(JSON.parse(data) as PhoneEvent)
}
