# Incoming-call ringtone

The top-bar ringtone control is available on every authenticated page. An unclaimed inbound call in `ringing` state plays two short tones every three seconds. The ringtone stops when the call is answered or ended, another browser claims its media session, the ringtone switch is disabled, or the authenticated page is closed. Outbound calls do not play this local ringtone.

## Enable and test

1. Refresh the page after upgrading.
2. If the top bar shows **启用铃声**, click it to enable sound. The page also tries restoring sound automatically when the browser already allows playback.
3. Click **试听铃声** to play a short preview, then call the modem's number from another phone.
4. Answer or end the call and verify the ringtone stops.

The switch defaults to enabled and is saved in browser local storage. Changes are synchronized between tabs of the same origin. When an incoming call arrives while browser audio is locked, a visible **立即响铃** button lets the user activate it.

## Browser behavior

Browsers can require user interaction before playing sound. A refresh cannot bypass that policy. Keep the page open, the tab unmuted, and the system sound output enabled. A sleeping or suspended browser may require another interaction to restore audio.

The ringtone uses Web Audio and does not request microphone access or create a WebRTC session. Each pulse has an audio-clock stop time so throttled JavaScript timers cannot leave a continuous tone playing. Answering or ending the call cancels scheduled pulses and stops active ones.

## Validation

Nine tests cover call eligibility, repeated tones, stopping, mute/re-enable, preview timeout, browser suspension, denied autoplay, disposal during activation, activation before a call and pending activation recovery. Run:

```sh
cd web
node ./node_modules/tsx/dist/cli.mjs --test tests/phoneRingtone.test.ts
npm run build
```

Manual acceptance: the user confirmed audible double-tone ringing on a real incoming call. Actual output volume and playback policy depend on the browser and system audio settings.
