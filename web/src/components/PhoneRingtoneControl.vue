<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { Alert24Regular, AlertOff24Regular } from '@vicons/fluent'
import { usePhoneStore } from '../stores/phone'
import { hasIncomingRingingCall, PhoneRingtone, RINGTONE_SETTING, type RingtoneState } from '../services/phone-ringtone'

const phone = usePhoneStore()
const enabled = ref(true)
try { enabled.value = localStorage.getItem(RINGTONE_SETTING) !== '0' } catch { /* Storage unavailable. */ }
const state = ref<RingtoneState>('locked')
const incoming = computed(() => hasIncomingRingingCall(phone.calls))
const engine = new PhoneRingtone({
  createContext: () => {
    const Context = window.AudioContext || (window as unknown as { webkitAudioContext: typeof AudioContext }).webkitAudioContext
    if (!Context) throw new Error('Web Audio unavailable')
    return new Context()
  },
  setTimeout: (callback, delay) => window.setTimeout(callback, delay),
  clearTimeout: (id) => window.clearTimeout(id)
}, (value) => { state.value = value })
engine.setEnabled(enabled.value)

watch(incoming, (ringing) => engine.setRinging(ringing), { immediate: true })
watch(enabled, (value) => {
  try { localStorage.setItem(RINGTONE_SETTING, value ? '1' : '0') } catch { /* Storage unavailable. */ }
  engine.setEnabled(value)
})

function unlock() {
  if (enabled.value) void engine.unlock()
}
function enableSound() {
  enabled.value = true
  engine.setEnabled(true)
  void engine.unlock()
}
function preview() {
  enabled.value = true
  engine.setEnabled(true)
  void engine.preview()
}
function syncPreference(event: StorageEvent) {
  if (event.key === RINGTONE_SETTING) enabled.value = event.newValue !== '0'
}

onMounted(() => {
  // Restore audio when the browser already permits playback.
  // A pending incoming call gets an explicit activation button if needed.
  unlock()
  window.addEventListener('pointerdown', unlock, { capture: true, passive: true })
  window.addEventListener('keydown', unlock, true)
  window.addEventListener('storage', syncPreference)
})
onUnmounted(() => {
  window.removeEventListener('pointerdown', unlock, true)
  window.removeEventListener('keydown', unlock, true)
  window.removeEventListener('storage', syncPreference)
  engine.dispose()
})
</script>

<template>
  <Teleport to="body">
    <div v-if="incoming && enabled && state !== 'ready'" class="ringtone-permission-alert" role="alert">
      <strong>有来电，浏览器尚未启用铃声</strong>
      <el-button v-if="state === 'locked'" type="primary" @click="enableSound">立即响铃</el-button>
      <span v-else>请换用支持声音播放的浏览器</span>
    </div>
  </Teleport>
  <el-popover placement="bottom-end" :width="280" trigger="click">
    <template #reference>
      <el-button text :aria-label="!enabled ? '来电铃声已关闭' : state === 'ready' ? '来电铃声已启用' : '点击启用来电声音'" :title="!enabled ? '来电铃声已关闭' : state === 'ready' ? '来电铃声已启用' : '点击启用来电声音'">
        <el-icon><Alert24Regular v-if="enabled" /><AlertOff24Regular v-else /></el-icon>
        <span class="ml-1">{{ enabled && state === 'locked' ? '启用铃声' : '铃声' }}</span>
        <span v-if="enabled && state !== 'ready'" class="ringtone-pending" aria-hidden="true" />
      </el-button>
    </template>
    <div class="ringtone-setting">
      <strong>来电铃声</strong>
      <el-switch v-model="enabled" aria-label="来电铃声" @change="unlock" />
    </div>
    <p class="ringtone-hint" role="status">
      {{ !enabled ? '铃声已关闭，来电仍会显示在页面。' : state === 'ready' ? '来电时播放铃声，接听或挂断后停止。' : state === 'unsupported' ? '当前浏览器无法播放铃声，请换用支持声音播放的浏览器。' : '请点击启用声音，允许此页面播放来电铃声。' }}
    </p>
    <div class="ringtone-actions">
      <el-button v-if="enabled && state === 'locked'" size="small" type="primary" @click="enableSound">启用声音</el-button>
      <el-button size="small" :disabled="state === 'unsupported'" @click="preview">试听铃声</el-button>
    </div>
    <p class="ringtone-hint">请保持页面打开。浏览器休眠或关闭页面后无法响铃。</p>
  </el-popover>
</template>

<style scoped>
.ringtone-permission-alert { position: fixed; top: 72px; left: 50%; transform: translateX(-50%); z-index: 2100; display: flex; align-items: center; gap: 16px; width: max-content; max-width: calc(100vw - 24px); padding: 14px 18px; border: 1px solid var(--el-color-warning); border-radius: 12px; background: var(--el-bg-color); color: var(--el-text-color-primary); box-shadow: 0 4px 24px rgba(0, 0, 0, .15); }
@media (max-width: 600px) { .ringtone-permission-alert { flex-wrap: wrap; justify-content: center; font-size: 14px; } }
.ringtone-setting { display: flex; align-items: center; justify-content: space-between; }
.ringtone-hint { font-size: 12px; line-height: 1.6; color: var(--el-text-color-secondary); margin: 10px 0; }
.ringtone-actions { display: flex; gap: 8px; }
.ringtone-pending { width: 6px; height: 6px; border-radius: 50%; background: var(--el-color-warning); margin-left: 5px; }
</style>
