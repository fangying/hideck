# HiDeck × DJI QDC507：上网、短信与按通话启用音频

> 型号为 QDC507，文件名保留 `hideck-qc507.md`。本文使用通用路径和占位符，不包含内部主机名、地址、凭据、手机号或通知服务配置。

## 项目与模块架构

HiDeck 是 Go 后端、Vue 前端的蜂窝模组管理服务，提供数据连接、代理、短信与浏览器电话。QDC507 是 DJI/Baiwang USB 复合模块，USB VID:PID 为 `2ca3:4006`；模块内部另有 Linux 系统及原生 IMS/VoLTE 栈，不等同于运行 HiDeck 的 Linux 宿主机。

| 通道 | 驱动/组件 | 职责 |
| --- | --- | --- |
| USB AT | option、HiDeck 唯一常驻 AT 管理器 | ATD/ATA/ATH 控制呼叫，CLCC 识别语音状态 |
| QMI | qmi_wwan、QMI WMS | 蜂窝数据会话、短信及注册相关查询 |
| ADB | 宿主 USB 用户态访问 | 加载模块内部语音驱动、配置 UCM 与音频路由 |
| UAC | snd-usb-audio、ALSA | 模块与宿主之间双向 PCM |
| WebRTC | Edge、Pion、PCMU/PCM 转换 | 浏览器麦克风/扬声器与宿主之间的音频 |

模块内部音频资产为 `qdc507_aprv3.ko`、`qdc507_voice.ko` 和 `mavo-pcm-bridge.armv7`。已有 runtime 校验 Linux 3.18.44、root 权限及资产哈希；这些不是所有 EC25 固件通用的安装包，也不是本次重新编译或分发的组件。

## 当前方案

部署保留 privileged 容器、host 网络、受信任 HTTPS 和 WebRTC UDP 7580。HTTPS 管理地址示例为 `https://<LAN_HOST_IP>:7576/#/phone`。防火墙保持原配置；不要求关闭 Wi-Fi，不改数据卷、证书或通知服务凭据。

上网由 QMI 数据会话和蜂窝网卡承载，短信由 QMI WMS 收发。此次音频固化不改变这两条业务路径；验证上网时应明确绑定蜂窝出口，不能用宿主 LAN 出口访问成功代替。

音频改为按通话启用，而不是开机常驻：

1. 响铃：建立 WebRTC 端点，模块 ALSA 保持关闭。
2. 接听：发送 ATA，确认 CLCC 为活动语音通话。
3. 准备：HiDeck 向宿主 Unix socket broker 申请独占音频路由。broker 启动现有 runtime，加载驱动、配置 UCM、建立 D4/UAC。
4. 传声：ready 后打开真实 ALSA，替换静音占位 PCM，双向传输。
5. 挂断：先关闭宿主 PCM，再释放 socket 占用，由 broker 清理模块 D4/UAC。
6. 异常：准备取消、容器退出和 broker 失联均有资源释放路径；路由或声卡失败不得伪装为正常音频。

这与 DJOneHub、MaVo、CellDock “通话进入活动态之后启动路由，再打开宿主 UAC”的原则一致。Linux 服务和浏览器媒体的生命周期由本项目独立实现。

完整安装、运行顺序、安全限制、排错和回滚见 [按通话音频方案](packaging/qdc507/CALL-SCOPED-AUDIO.md)。

## 固化组件

| 组件 | 生命周期 |
| --- | --- |
| USB watch / boot-reset | 修正 DJI 接口归属；主机启动时执行一次模块软件恢复 |
| audio-broker.service | 开机常驻，root-only Unix socket，不增加 TCP 监听 |
| audio.service | 由 broker 按需启动；待机 inactive 是正常状态 |
| audio-runtime | 校验资产、ADB 初始化、UCM 校准、D4/UAC 与 ready |
| route_lease.go | 通话申请、取消、真实 PCM 包装及关闭后释放 |
| 电话事件流 | 页面恢复焦点、网络或可见性时重连；心跳超时后重试 |

容器继续只读挂载整个 `/run/hideck-qdc507-audio` 目录。该目录包含 `managed`、`control.sock` 和按通话出现的 `ready`；不要单独挂载 socket，也不要在运行中删除重建目录。当前 socket 仅允许 root 连接，适用于现有 root 容器；非 root 部署需另行设计权限。

## 主机重启后的自恢复

设计顺序为：

USB 接口校正 → 模块 boot-reset → broker 待机 → HiDeck 恢复 QMI/短信/AT 监听 → 通话 active → 启动音频路由 → ALSA/WebRTC。

主机重启不保证模块断电；boot-reset 处理此前观察到的模块状态残留。音频 broker 开机后不应保持 D4 运行，也不应要求待机存在 ready 文件。

原常驻方案此前有温重启、上网和真人通话记录，但本轮更换了音频生命周期：**新 broker 版本已通过呼入、外呼的手机端听测，整机重启恢复和手机到电脑的语音内容仍待验收**。不能把旧版的成功当作新版已完成验收。ADB 完全失联、USB 硬件或供电故障不保证免除现场维修。

## 本轮证据与边界

| 项目 | 当前证据 |
| --- | --- |
| 外部呼入，接听侧到手机 | 临时脚本两次听测通过；正式 broker 版本两次听测通过，最新复测用户确认声音清晰 |
| 主动外呼，接听侧到手机 | 正式 broker 版本从 Edge 主动拨号，两次听测通过，最新复测用户确认声音清楚 |
| 接听时限 | 第二次从首次 RING 到 active 约 17 秒；不构成 GUI 操作今后必达 20 秒的保证 |
| 手机到接听侧 | 真实模块 PCM 非零且有明显幅值变化；未独立核验录音中的语音内容 |
| 新 broker 固化实现 | 前端 314 项测试、后端相关测试及 race 检查通过；容器内 socket、释放清理、容器重启、broker 重启均检查通过；呼入和外呼手机端听测均通过 |
| 上网、短信 | 有前期验证记录，本轮没有重新做业务验收 |
| 重启与长期稳定性 | 新版多轮重启、冷启动、连续通话和异常中断仍待验证 |

## 调试中的坑

- **静音占位误判为硬件静音**：QPCMV 失败后旧路径返回 nullPCM；零样本由应用生成，不能据此断定模块没有音频。QDC507 当前不用 QPCMV 接通声音。
- **RTP 头误当 PCMU**：必须正确处理扩展头、CSRC 和 padding；现改用 Pion RTP 解包。
- **路由启动太早**：WebRTC connected 或 runtime ready 不证明蜂窝声音已进入路由；本轮有效顺序是活动语音态后重新准备路由。
- **QMI 与 AT 混用状态**：QDC507 使用 CLCC 语音编号；mode=1 是数据会话。不要让 QMI 空快照取消真实 AT 来电。
- **多个串口读者**：调试 socat 等程序会抢走 URC/响应；统一由 AT 管理器串行调度。ATA 成功的终止 OK 被管理器消费，不应再次要求返回 payload 包含 OK。
- **来电事件与点击延迟不同**：Edge 曾收不到更新，也曾已显示按钮但工具因窗口变化拒绝点击。事件流恢复不能解决全部 UI 延迟。
- **网络与模块分层排查**：先查 HTTPS、UDP 7580 和 ICE，再查真实 ALSA、UAC 与 D4。没有依据把 Wi-Fi 或防火墙视为这次路由问题的根因。
- **不要夸大稳定性**：正式版本呼入、外呼各两次听测通过，但没有完成长时间、重启和全部双向内容验收；初始化通常还需数秒，失败必须显式反馈。

代码与配置不包含内部部署凭据、短信内容、号码或 Bark/Memos 配置。详细实验日志与录音不应直接提交到公共仓库。
