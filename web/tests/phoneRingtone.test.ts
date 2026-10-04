import { test } from 'node:test'
import assert from 'node:assert/strict'
import { hasIncomingRingingCall, PhoneRingtone } from '../src/services/phone-ringtone'

function fixture() {
  let next = 0
  const timers = new Map<number, { callback: () => void; delay: number }>()
  const tones: { stopped: number; frequency: { value: number }; start: (time: number) => void; stop: (time?: number) => void; connect: () => void; disconnect: () => void; onended: (() => void) | null }[] = []
  const states: string[] = []
  const context = {
    state: 'suspended', currentTime: 0, destination: {}, onstatechange: null as (() => void) | null,
    async resume() { this.state = 'running' },
    async close() { this.state = 'closed' },
    createGain() { return { gain: { setValueAtTime() {}, linearRampToValueAtTime() {} }, connect() {}, disconnect() {} } },
    createOscillator() {
      const tone = { stopped: 0, frequency: { value: 0 }, onended: null as (() => void) | null,
        start() {}, stop(time?: number) { if (time === undefined) this.stopped++ }, connect() {}, disconnect() {} }
      tones.push(tone)
      return tone
    }
  }
  const engine = new PhoneRingtone({
    createContext: () => context as unknown as AudioContext,
    setTimeout(callback, delay) { const id = ++next; timers.set(id, { callback, delay }); return id },
    clearTimeout(id) { timers.delete(id) }
  }, (state) => states.push(state))
  function fire(delay: number) {
    const entry = [...timers].find(([, timer]) => timer.delay === delay)
    assert.ok(entry); timers.delete(entry[0]); entry[1].callback()
  }
  return { engine, timers, tones, states, context, fire }
}

test('only unclaimed inbound ringing calls trigger the ringtone', () => {
  const call = { direction: 'inbound' as const, status: 'ringing' as const, media_id: '' }
  assert.equal(hasIncomingRingingCall([call]), true)
  for (const status of ['calling', 'connected', 'completed', 'waiting'] as const) {
    assert.equal(hasIncomingRingingCall([{ ...call, status }]), false)
  }
  assert.equal(hasIncomingRingingCall([{ ...call, direction: 'outbound' }]), false)
  assert.equal(hasIncomingRingingCall([{ ...call, media_id: 'answered-by-another-browser' }]), false)
  assert.equal(hasIncomingRingingCall([]), false)
})

test('pending call rings after gesture unlock, repeats, and stops immediately when answered', async () => {
  const f = fixture()
  f.engine.setRinging(true)
  assert.equal(f.tones.length, 0)
  assert.equal(await f.engine.unlock(), true)
  assert.equal(f.tones.length, 2)
  await f.engine.unlock()
  assert.equal(f.tones.length, 2, 'repeated gestures must not duplicate the loop')
  f.fire(3000)
  assert.equal(f.tones.length, 4)
  f.engine.setRinging(false)
  assert.equal(f.timers.size, 0)
  assert.ok(f.tones.every((tone) => tone.stopped === 1))
  f.engine.dispose()
})

test('mute stops a call and re-enabling resumes a still-pending call', async () => {
  const f = fixture(); await f.engine.unlock(); f.engine.setRinging(true)
  f.engine.setEnabled(false)
  assert.equal(f.timers.size, 0)
  assert.ok(f.tones.every((tone) => tone.stopped === 1))
  f.engine.setEnabled(true)
  assert.equal(f.tones.length, 4)
  f.engine.dispose()
  assert.equal(f.timers.size, 0)
  assert.equal(f.context.state, 'closed')
})

test('preview ends automatically, while a real pending call keeps ringing', async () => {
  const f = fixture(); assert.equal(await f.engine.preview(), true)
  f.fire(1500); assert.equal(f.timers.size, 0)
  f.engine.setRinging(true); await f.engine.preview(); f.fire(1500)
  assert.equal(f.timers.size, 1)
  f.engine.setRinging(false); assert.equal(f.timers.size, 0)
  f.engine.dispose()
})

test('browser suspension stops scheduling and gesture resume restarts the pending call', async () => {
  const f = fixture(); await f.engine.unlock(); f.engine.setRinging(true)
  f.context.state = 'suspended'; f.context.onstatechange?.()
  assert.equal(f.timers.size, 0)
  assert.equal(f.states.at(-1), 'locked')
  await f.engine.unlock(); assert.equal(f.timers.size, 1)
  f.engine.dispose()
})

test('denied playback does not ring and can recover after a later gesture', async () => {
  const f = fixture(); const resume = f.context.resume
  f.context.resume = async () => { throw new Error('NotAllowedError') }
  f.engine.setRinging(true); assert.equal(await f.engine.unlock(), false)
  assert.equal(f.tones.length, 0)
  f.context.resume = resume; assert.equal(await f.engine.unlock(), true)
  assert.equal(f.tones.length, 2)
  f.engine.dispose()
})

test('unmount during a pending unlock cannot start tones or timers', async () => {
  const f = fixture(); let resolve!: () => void
  f.context.resume = () => new Promise<void>((r) => { resolve = r })
  f.engine.setRinging(true); const pending = f.engine.unlock(); f.engine.dispose(); resolve()
  assert.equal(await pending, false); assert.equal(f.timers.size, 0); assert.equal(f.tones.length, 0)
})

test('a browser that already allows playback can enable audio at page startup before any call', async () => {
  const f = fixture()
  assert.equal(await f.engine.unlock(), true)
  assert.equal(f.tones.length, 0, 'startup must stay silent without a call')
  f.engine.setRinging(true)
  assert.equal(f.tones.length, 2, 'an incoming event starts sound without another gesture')
  f.engine.dispose()
})

test('a pending autoplay resume starts the incoming ringtone when browser activation arrives', async () => {
  const f = fixture(); let resolve!: () => void
  f.context.resume = () => new Promise<void>((r) => { resolve = r })
  const pending = f.engine.unlock(); f.engine.setRinging(true)
  assert.equal(f.tones.length, 0)
  f.context.state = 'running'; f.context.onstatechange?.()
  assert.equal(f.tones.length, 2)
  resolve(); assert.equal(await pending, true)
  assert.equal(f.tones.length, 2, 'activation completion must not duplicate tones')
  f.engine.dispose()
})
