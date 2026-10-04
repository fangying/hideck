import type { PhoneCall } from './phone'

export type RingtoneState = 'locked' | 'ready' | 'unsupported'
export const RINGTONE_SETTING = 'hideck.phone.ringtone.enabled'

export function hasIncomingRingingCall(calls: Pick<PhoneCall, 'direction' | 'status' | 'media_id'>[]): boolean {
  return calls.some((call) => call.direction === 'inbound' && call.status === 'ringing' && !call.media_id)
}

type Dependencies = {
  createContext: () => AudioContext
  setTimeout: (callback: () => void, delay: number) => number
  clearTimeout: (id: number) => void
}

// A local alert, independent of the call's microphone and WebRTC connection.
export class PhoneRingtone {
  private context: AudioContext | null = null
  private ringing = false
  private enabled = true
  private disposed = false
  private previewing = false
  private timer: number | null = null
  private previewTimer: number | null = null
  private tones = new Set<OscillatorNode>()
  private state: RingtoneState = 'locked'

  constructor(private dependencies: Dependencies, private onState: (state: RingtoneState) => void) {}

  async unlock(): Promise<boolean> {
    if (this.disposed) return false
    try {
      if (!this.context) {
        this.context = this.dependencies.createContext()
        this.context.onstatechange = () => this.sync()
      }
      // Called directly from a user gesture, including the explicit enable button.
      await this.context.resume()
      if (this.disposed) return false
      this.sync()
      return this.context.state === 'running'
    } catch {
      this.updateState(this.context ? 'locked' : 'unsupported')
      return false
    }
  }

  setEnabled(enabled: boolean) {
    this.enabled = enabled
    if (!enabled) this.endPreview()
    this.sync()
  }

  setRinging(ringing: boolean) {
    this.ringing = ringing
    this.sync()
  }

  async preview(): Promise<boolean> {
    if (!this.enabled || !await this.unlock() || this.disposed || !this.enabled) return false
    this.endPreview()
    this.previewing = true
    this.sync()
    this.previewTimer = this.dependencies.setTimeout(() => this.endPreview(), 1_500)
    return true
  }

  dispose() {
    this.disposed = true
    this.endPreview()
    this.stopTones()
    if (this.context) {
      this.context.onstatechange = null
      void this.context.close().catch(() => {})
    }
  }

  private updateState(state: RingtoneState) {
    if (this.state === state) return
    this.state = state
    this.onState(state)
  }

  private sync() {
    if (this.disposed) return
    const ready = this.context?.state === 'running'
    this.updateState(ready ? 'ready' : this.state === 'unsupported' ? 'unsupported' : 'locked')
    if (this.enabled && ready && (this.ringing || this.previewing)) {
      if (this.timer === null) this.ring()
    } else this.stopTones()
  }

  private ring() {
    const context = this.context!
    // Two short pulses every three seconds. Schedule on the audio clock so a
    // background tab's throttled JS timer cannot leave a continuous tone playing.
    for (const offset of [0, 0.6]) {
      const start = context.currentTime + offset
      const gain = context.createGain()
      gain.gain.setValueAtTime(0, start)
      gain.gain.linearRampToValueAtTime(0.08, start + 0.02)
      gain.gain.setValueAtTime(0.08, start + 0.38)
      gain.gain.linearRampToValueAtTime(0, start + 0.4)
      gain.connect(context.destination)
      const oscillator = context.createOscillator()
      oscillator.frequency.value = 660
      oscillator.connect(gain)
      this.tones.add(oscillator)
      oscillator.onended = () => {
        this.tones.delete(oscillator)
        oscillator.disconnect()
        gain.disconnect()
      }
      oscillator.start(start)
      oscillator.stop(start + 0.41)
    }
    this.timer = this.dependencies.setTimeout(() => {
      this.timer = null
      this.sync()
    }, 3_000)
  }

  private endPreview() {
    if (this.previewTimer !== null) this.dependencies.clearTimeout(this.previewTimer)
    this.previewTimer = null
    this.previewing = false
    this.sync()
  }

  private stopTones() {
    if (this.timer !== null) this.dependencies.clearTimeout(this.timer)
    this.timer = null
    for (const tone of this.tones) {
      try { tone.stop() } catch { /* Already ended. */ }
    }
    this.tones.clear()
  }
}
