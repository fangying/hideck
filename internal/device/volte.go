package device

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/iniwex5/quectel-qmi-go/pkg/qmi"
	"github.com/yibaiba/hideck/internal/modem"
	"github.com/yibaiba/hideck/internal/volte"
	"github.com/yibaiba/hideck/pkg/logger"
)

func (p *Pool) NativeVoLTEController() *volte.Controller {
	if p == nil {
		return nil
	}
	return p.volteCtl
}

func (p *Pool) IsNativeVoLTE(deviceID string) bool {
	w := p.GetWorker(strings.TrimSpace(deviceID))
	if w == nil {
		return false
	}
	return IsNativeVoLTEMode(w.Config.PhoneMode) && PhoneServiceEnabled(w.Config)
}

func (p *Pool) NativeVoLTEStatus(deviceID string) volte.Status {
	if p == nil || p.volteCtl == nil {
		return volte.Status{DeviceID: deviceID, Phase: volte.PhaseIdle}
	}
	return p.volteCtl.Status(deviceID)
}

func (p *Pool) RestoreNativeVoLTE(ctx context.Context, deviceID string) error {
	if p == nil || p.volteCtl == nil {
		return fmt.Errorf("VoLTE 控制器未初始化")
	}
	return p.volteCtl.Restore(ctx, strings.TrimSpace(deviceID))
}

func (p *Pool) EnableNativeVoLTE(deviceID string) error {
	if p == nil || p.volteCtl == nil {
		return fmt.Errorf("VoLTE 控制器未初始化")
	}
	deviceID = strings.TrimSpace(deviceID)
	w := p.GetWorker(deviceID)
	if w == nil {
		return fmt.Errorf("设备 %s 不存在", deviceID)
	}
	if p.IsESIMSwitching(deviceID) {
		return fmt.Errorf("设备 %s 正在切卡，暂不允许启动 VoLTE", deviceID)
	}
	class, err := ClassifyWorkerLebaraUKForControl(p.Context(), w)
	if err != nil {
		return err
	}
	if class.BlocksVoWiFi() || class.IsLebara {
		return ErrLebaraUKRFLocked
	}
	if err := p.waitQMICoreReady(deviceID, 30*time.Second); err != nil {
		logger.Warn("VoLTE 等待 QMI 就绪失败，继续尝试 AT", "device", deviceID, "err", err)
	}
	if err := p.volteCtl.Enable(p.Context(), deviceID); err != nil {
		return err
	}
	// IMS PDN 起来后 qmi_wwan 可能把上网口拉起来。未开「网络」时主机不能走 3gnet。
	if w := p.GetWorker(deviceID); w != nil && !w.Config.NetworkEnabled {
		if err := p.applyNetworkPreference(w); err != nil {
			logger.Warn("VoLTE 已启用，抑制上网数据失败", "device", deviceID, "err", err)
		}
	}
	return nil
}

func (p *Pool) ScheduleNativeVoLTE(deviceID, reason string) {
	p.scheduleNativeVoLTE(deviceID, reason)
}

const nativeVoLTEStartAttempts = 3

func nativeVoLTERetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	return time.Duration(attempt) * 3 * time.Second
}

func isTransientVoLTEStartError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, volte.ErrNoUniqueProfile) ||
		errors.Is(err, volte.ErrVIDPIDMismatch) ||
		errors.Is(err, ErrLebaraUKRFLocked) {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "timed out") ||
		strings.Contains(msg, "busy") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection closed") ||
		strings.Contains(msg, "qmi-proxy") ||
		strings.Contains(msg, "service not ready") ||
		strings.Contains(msg, "服务未就绪")
}

func (p *Pool) scheduleNativeVoLTE(deviceID, reason string) {
	if p == nil || strings.TrimSpace(deviceID) == "" {
		return
	}
	deviceID = strings.TrimSpace(deviceID)
	if !p.beginNativeVoLTESchedule(deviceID) {
		return
	}
	go func() {
		defer p.endNativeVoLTESchedule(deviceID)
		var err error
		for attempt := 1; attempt <= nativeVoLTEStartAttempts; attempt++ {
			if !p.IsNativeVoLTE(deviceID) {
				return
			}
			err = p.EnableNativeVoLTE(deviceID)
			if err == nil {
				return
			}
			if !isTransientVoLTEStartError(err) || attempt == nativeVoLTEStartAttempts {
				logger.Warn("启动原生 VoLTE 失败", "device", deviceID, "reason", reason, "attempt", attempt, "err", err)
				return
			}
			logger.Warn("启动原生 VoLTE 将重试", "device", deviceID, "reason", reason, "attempt", attempt, "err", err)
			timer := time.NewTimer(nativeVoLTERetryDelay(attempt))
			select {
			case <-p.Context().Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
}

func (p *Pool) beginNativeVoLTESchedule(deviceID string) bool {
	p.nativeVoLTEScheduleMu.Lock()
	defer p.nativeVoLTEScheduleMu.Unlock()
	if p.nativeVoLTEScheduled == nil {
		p.nativeVoLTEScheduled = make(map[string]struct{})
	}
	if _, exists := p.nativeVoLTEScheduled[deviceID]; exists {
		return false
	}
	p.nativeVoLTEScheduled[deviceID] = struct{}{}
	return true
}

func (p *Pool) endNativeVoLTESchedule(deviceID string) {
	p.nativeVoLTEScheduleMu.Lock()
	delete(p.nativeVoLTEScheduled, deviceID)
	p.nativeVoLTEScheduleMu.Unlock()
}

func (p *Pool) stopNativeVoLTE(deviceID, reason string) {
	if p == nil || p.volteCtl == nil {
		return
	}
	p.volteCtl.Disable(deviceID)
	logger.Debug("已停止原生 VoLTE 会话", "device", deviceID, "reason", reason)
}

func (p *Pool) ExecuteAT(deviceID, cmd string, timeout time.Duration) (string, error) {
	w := p.GetWorker(deviceID)
	if w == nil {
		return "", fmt.Errorf("设备 %s 不存在", deviceID)
	}
	// QMI devices with cellular calling keep a resident AT manager. Reuse its
	// serialized command scheduler; opening a second tty reader makes CEREG,
	// COPS and call URCs fail with "serial port busy".
	if w.Modem != nil && w.Modem.CanExecuteAT() {
		return w.Modem.ExecuteAT(cmd, timeout)
	}
	port := strings.TrimSpace(w.ResolvedATPort())
	if port == "" {
		return "", fmt.Errorf("设备 %s 没有 AT 口", deviceID)
	}
	unlock := p.lockDeviceAT(deviceID)
	defer unlock()
	session, err := modem.NewSerialAT(port, 115200, 8, 1, "N")
	if err != nil {
		return "", fmt.Errorf("打开 AT 口 %s: %w", port, err)
	}
	defer session.Close()
	return session.Execute(cmd, timeout)
}

func (p *Pool) lockDeviceAT(deviceID string) func() {
	p.atPortMu.Lock()
	if p.atPortLocks == nil {
		p.atPortLocks = map[string]*sync.Mutex{}
	}
	mu := p.atPortLocks[deviceID]
	if mu == nil {
		mu = &sync.Mutex{}
		p.atPortLocks[deviceID] = mu
	}
	p.atPortMu.Unlock()
	mu.Lock()
	return mu.Unlock
}

func (p *Pool) StopSoftwareIMS(deviceID string) error {
	if p == nil {
		return nil
	}
	if !p.IsVoWiFiActive(deviceID) && p.GetVoWiFiAppForDevice(deviceID) == nil {
		return nil
	}
	return p.voWiFiHost().Disable(p.Context(), deviceID, "native_volte", true)
}

func (p *Pool) SetNativeIMS(ctx context.Context, deviceID string, enabled bool) error {
	w := p.GetWorker(deviceID)
	if w == nil || w.QMICore == nil {
		return fmt.Errorf("设备 %s 没有 QMI", deviceID)
	}
	return w.QMICore.SetIMSServiceEnabled(ctx, enabled)
}

func (p *Pool) EnsureIMSClients(ctx context.Context, deviceID string) error {
	w := p.GetWorker(deviceID)
	if w == nil || w.QMICore == nil {
		return fmt.Errorf("设备 %s 没有 QMI", deviceID)
	}
	return w.QMICore.EnsureIMSClients(ctx)
}

func (p *Pool) ReleaseIMSClients(deviceID string) error {
	w := p.GetWorker(deviceID)
	if w == nil || w.QMICore == nil {
		return nil
	}
	return w.QMICore.ReleaseIMSClients()
}

func (p *Pool) OnIMSRegistration(deviceID string, handler func(*qmi.IMSARegistrationStatus)) error {
	w := p.GetWorker(deviceID)
	if w == nil || w.QMICore == nil {
		return fmt.Errorf("设备 %s 没有 QMI", deviceID)
	}
	return w.QMICore.OnIMSRegistrationStatus(handler)
}

func (p *Pool) OnIMSServices(deviceID string, handler func(*qmi.IMSAServicesStatus)) error {
	w := p.GetWorker(deviceID)
	if w == nil || w.QMICore == nil {
		return fmt.Errorf("设备 %s 没有 QMI", deviceID)
	}
	return w.QMICore.OnIMSServicesStatus(handler)
}

func (p *Pool) IMSAStatus(ctx context.Context, deviceID string) (volte.Registration, error) {
	w := p.GetWorker(deviceID)
	if w == nil || w.QMICore == nil {
		return volte.Registration{}, fmt.Errorf("设备 %s 没有 QMI", deviceID)
	}
	reg, err := w.QMICore.IMSAGetIMSRegistrationStatus(ctx)
	if err != nil {
		return volte.Registration{}, err
	}
	out := volte.Registration{Registered: reg != nil && reg.HasStatus &&
		(reg.Status == qmi.IMSARegistrationStateRegistered || reg.Status == qmi.IMSARegistrationStateLimitedRegistered)}
	svc, svcErr := w.QMICore.IMSAGetIMSServicesStatus(ctx)
	if svcErr == nil && svc != nil && svc.HasVoiceServiceStatus {
		out.VoiceAvailable = svc.VoiceServiceStatus == qmi.IMSAServiceAvailabilityAvailable
	}
	return out, nil
}

func (p *Pool) AudioDevice(deviceID string) string {
	w := p.GetWorker(deviceID)
	if w == nil {
		return ""
	}
	if p.USBAudioUnusable(deviceID) {
		w.Config.AudioDevice = ""
		return ""
	}
	if dev := strings.TrimSpace(w.Config.AudioDevice); dev != "" {
		return dev
	}
	usbPath := strings.TrimSpace(w.Config.USBPath)
	if usbPath == "" {
		return ""
	}
	dev, _ := findAudioDevice(usbPath)
	if dev != "" {
		w.Config.AudioDevice = dev
	}
	return dev
}

func (p *Pool) USBAudioUnusable(deviceID string) bool {
	w := p.GetWorker(deviceID)
	if w == nil {
		return false
	}
	return modemUACUnusable(w.Config.USBPath)
}

func (p *Pool) USBResidentAudioReady(deviceID string) bool {
	w := p.GetWorker(deviceID)
	if w == nil {
		return false
	}
	return qdc507ResidentAudioReady(w.Config.USBPath)
}

func (p *Pool) VOICEDial(ctx context.Context, deviceID, number string) (uint8, error) {
	w := p.GetWorker(deviceID)
	if w == nil || w.QMICore == nil {
		return 0, fmt.Errorf("设备 %s 没有 QMI VOICE", deviceID)
	}
	if p.useQDC507ATVoice(w) {
		if err := p.executeQDC507VoiceAT(deviceID, fmt.Sprintf("ATD%s;", number), 60*time.Second); err != nil {
			return 0, err
		}
		callID, err := p.findATVoiceCallID(ctx, w, number)
		if err != nil {
			// Do not report a successful call with the sentinel QMI ID 0: the
			// voice controller would be unable to reconcile later QMI events.
			cleanupErr := p.executeQDC507VoiceAT(deviceID, "ATH", 3*time.Second)
			if cleanupErr != nil {
				return 0, errors.Join(err, fmt.Errorf("清理未关联的 AT 呼叫失败: %w", cleanupErr))
			}
			return 0, err
		}
		return callID, nil
	}
	return w.QMICore.VOICEDialCall(ctx, number)
}

func (p *Pool) VOICEAnswer(ctx context.Context, deviceID string, callID uint8) error {
	w := p.GetWorker(deviceID)
	if w == nil || w.QMICore == nil {
		return fmt.Errorf("设备 %s 没有 QMI VOICE", deviceID)
	}
	if p.useQDC507ATVoice(w) {
		return p.executeQDC507VoiceAT(deviceID, "ATA", 5*time.Second)
	}
	_, err := w.QMICore.VOICEAnswerCall(ctx, callID)
	return err
}

func (p *Pool) VOICEHangup(ctx context.Context, deviceID string, callID uint8) error {
	w := p.GetWorker(deviceID)
	if w == nil || w.QMICore == nil {
		return fmt.Errorf("设备 %s 没有 QMI VOICE", deviceID)
	}
	if p.useQDC507ATVoice(w) {
		return p.executeQDC507VoiceAT(deviceID, "ATH", 3*time.Second)
	}
	_, err := w.QMICore.VOICEEndCall(ctx, callID)
	return err
}

// QDC507/Baiwang removes the modem's original audio route. Its proven call
// path uses USB AT for call control while QMI remains the IMS/status channel.
func (p *Pool) useQDC507ATVoice(w *Worker) bool {
	return w != nil && isBaiwangUSB(strings.TrimSpace(w.Config.USBPath))
}

// UseATVoiceStatus reports whether AT+CLCC must be treated as the authoritative
// call-state source. QDC507 firmware can deliver RING/CLCC on the AT port while
// omitting the corresponding QMI VOICE indication.
func (p *Pool) UseATVoiceStatus(deviceID string) bool {
	return p != nil && p.useQDC507ATVoice(p.GetWorker(strings.TrimSpace(deviceID)))
}

// executeQDC507VoiceAT uses Pool.ExecuteAT so QDC507 call control works in
// both mixed AT mode and pure QMI mode. Pure QMI intentionally has no resident
// AT manager; ExecuteAT then opens the configured port for one serialized
// command instead of failing with "AT manager not started".
func (p *Pool) executeQDC507VoiceAT(deviceID, command string, timeout time.Duration) error {
	w := p.GetWorker(deviceID)
	if w != nil && w.Modem != nil && w.Modem.CanExecuteAT() {
		// Manager consumes the terminal OK and returns an empty payload for
		// successful ATA/ATH. Its error result is the success authority.
		_, err := w.Modem.ExecuteAT(command, timeout)
		return err
	}
	response, err := p.ExecuteAT(deviceID, command, timeout)
	if err != nil {
		return fmt.Errorf("QDC507 语音 AT %s 失败: %w", command, err)
	}
	return validateQDC507VoiceATResponse(command, response)
}

func validateQDC507VoiceATResponse(command, response string) error {
	upper := strings.ToUpper(response)
	if strings.Contains(upper, "ERROR") || strings.Contains(upper, "NO CARRIER") {
		return fmt.Errorf("QDC507 语音 AT %s 被模组拒绝: %s", command, strings.TrimSpace(response))
	}
	if !strings.Contains(upper, "OK") && !strings.Contains(upper, "CONNECT") {
		return fmt.Errorf("QDC507 语音 AT %s 返回异常: %s", command, strings.TrimSpace(response))
	}
	return nil
}

func (p *Pool) findATVoiceCallID(ctx context.Context, w *Worker, number string) (uint8, error) {
	if w == nil || w.Modem == nil || !w.Modem.CanExecuteAT() {
		return 0, fmt.Errorf("QDC507 AT 呼叫没有 AT 状态通道")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	for {
		if response, err := w.Modem.ExecuteAT("AT+CLCC", time.Second); err == nil {
			if id, ok := outboundCLCCID(response, number); ok {
				return id, nil
			}
		}
		select {
		case <-ctx.Done():
			return 0, fmt.Errorf("等待 QDC507 AT 呼叫 call ID 被取消: %w", ctx.Err())
		case <-deadline.C:
			return 0, fmt.Errorf("QDC507 AT 呼叫已发出，但 3 秒内未获得匹配的 CLCC voice call ID")
		case <-ticker.C:
		}
	}
}

func outboundCLCCID(response, number string) (uint8, bool) {
	var candidates []uint8
	for _, line := range strings.Split(strings.ReplaceAll(response, "\r", ""), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "+CLCC:") {
			continue
		}
		r := csv.NewReader(strings.NewReader(strings.TrimSpace(strings.TrimPrefix(line, "+CLCC:"))))
		r.TrimLeadingSpace = true
		f, err := r.Read()
		if err != nil || len(f) < 5 || f[1] != "0" || f[3] != "0" || (f[2] != "0" && f[2] != "2" && f[2] != "3") {
			continue
		}
		id, err := strconv.ParseUint(f[0], 10, 8)
		if err != nil || id == 0 {
			continue
		}
		if len(f) > 5 && f[5] != "" {
			if sameVoiceNumber(f[5], number) {
				return uint8(id), true
			}
			continue
		}
		candidates = append(candidates, uint8(id))
	}
	if len(candidates) == 1 {
		return candidates[0], true
	}
	return 0, false
}

func sameVoiceNumber(a, b string) bool {
	clean := func(s string) string {
		var out strings.Builder
		for _, r := range s {
			if r >= '0' && r <= '9' {
				out.WriteRune(r)
			}
		}
		return out.String()
	}
	a, b = clean(a), clean(b)
	if a == b {
		return true
	}
	if len(a) > 11 {
		a = a[len(a)-11:]
	}
	if len(b) > 11 {
		b = b[len(b)-11:]
	}
	return a == b
}

func (p *Pool) VOICEManageCalls(ctx context.Context, deviceID string, req qmi.VoiceManageCallsRequest) error {
	w := p.GetWorker(deviceID)
	if w == nil || w.QMICore == nil {
		return fmt.Errorf("设备 %s 没有 QMI VOICE", deviceID)
	}
	return w.QMICore.VOICEManageCalls(ctx, req)
}

func (p *Pool) VOICEBurstDTMF(ctx context.Context, deviceID string, callID uint8, digits string) error {
	w := p.GetWorker(deviceID)
	if w == nil || w.QMICore == nil {
		return fmt.Errorf("设备 %s 没有 QMI VOICE", deviceID)
	}
	_, err := w.QMICore.VOICEBurstDTMF(ctx, callID, digits)
	return err
}

func (p *Pool) OnVoiceStatus(deviceID string, handler func(*qmi.VoiceAllCallInfo)) error {
	w := p.GetWorker(deviceID)
	if w == nil || w.QMICore == nil {
		return fmt.Errorf("设备 %s 没有 QMI VOICE", deviceID)
	}
	return w.QMICore.OnVoiceCallStatus(handler)
}

func (p *Pool) VOICEGetAllCallInfo(ctx context.Context, deviceID string) (*qmi.VoiceAllCallInfo, error) {
	w := p.GetWorker(deviceID)
	if w == nil || w.QMICore == nil {
		return nil, fmt.Errorf("设备 %s 没有 QMI VOICE", deviceID)
	}
	return w.QMICore.VOICEGetAllCallInfo(ctx)
}
