# QDC507 按通话生命周期管理音频

## 结论与验收边界

本轮连续两次外部呼入，接听后从浏览器所在 Mac 扬声器播放测试语音，手机端均确认听到。第二次从后台首次 RING 到通话 active 约 17 秒。模块返回的真实 PCM 不再全零，出现随讲话变化的幅值；本轮没有独立录音转写验证手机到浏览器的语音内容，不能仅凭峰值宣布双向长期稳定。

这两次真人测试先使用临时脚本验证生命周期。随后部署正式 Unix socket broker，重启前完成呼入、外呼各两次手机端听测；随后完成两轮整机温重启，每轮后的外呼及第二轮后的呼入均由用户确认声音清楚。正式版本不再依赖临时日志监听脚本。冷启动、短信与蜂窝出口业务不能沿用旧版本的验收结论；手机到电脑的语音内容及长时间稳定性仍需单独核验。

## 为什么改变原方案

旧方案在主机开机后常驻 D4/UAC 路由。实验改为：响铃时不占用模块 PCM，ATA 成功并确认 CLCC 的活动语音态后，重新准备 UCM、启动 D4/UAC，再打开宿主 ALSA。挂断先关宿主声卡，再清理模块路由。两次复测均恢复了手机端可听语音。

还修复了两个独立问题：不支持的 `AT+QPCMV=1,2` 曾让应用降级到 `nullPCM`，由应用产生的零样本不能作为模块故障证据；RTP 手工截取忽略扩展头和 padding，曾把非音频字节误解码成 PCMU。当前使用 Pion RTP 解包。

这支持“路由生命周期是关键因素”的判断，不等于已证明固件内部的全部根因。防火墙与 Wi-Fi 没有因为本轮修复而变更。

## 三条业务链路

| 功能 | 路径 | 本轮是否改动 |
| --- | --- | --- |
| 上网 | HiDeck → QMI 数据会话 → qmi_wwan → 蜂窝网络 | 不改变数据路径 |
| 短信 | HiDeck → QMI WMS → 运营商短信 | 不改变收发与通知配置 |
| 通话控制 | 浏览器 → HiDeck → 唯一常驻 AT 管理器 → ATA/ATD/ATH；CLCC 判定语音状态 | 统一 AT 编号与状态，过滤 mode=1 数据会话 |
| 通话音频 | 浏览器 WebRTC ↔ PCMU/PCM ↔ 宿主 ALSA/UAC ↔ 模块 D4/语音驱动 ↔ VoLTE | 改为通话接通后申请路由，结束后释放 |

ADB 只负责模块内部准备和路由，不传浏览器的实时语音。`qdc507_aprv3.ko`、`qdc507_voice.ko`、`mavo-pcm-bridge.armv7` 仍使用现有 runtime 固定哈希校验的资产，不在本次重新打包分发。

DJOneHub、MaVo、CellDock 的共同参考原则是“活动语音通话后启动路由，再开宿主 UAC”。本次独立实现 Linux 服务生命周期，不照搬它们的 macOS/CoreAudio 实现或第三方二进制授权。

## 固化的运行顺序

1. 待机：broker 运行，模块 D4/UAC 路由关闭；来电时创建 WebRTC 媒体端点，但不打开模块 PCM。
2. 接听：HiDeck 发送 ATA，确认 CLCC 活动语音态。此时运营商通话已接通，不等待音频初始化才发送 ATA。
3. 申请：HiDeck 连接 `/run/hideck-qdc507-audio/control.sock`。连接本身就是本通话的占用凭据；broker 不接收 shell 命令、路径或任意参数。
4. 准备：broker 独占锁定模块，验证旧 D4 已关闭、UAC 已禁用，启动音频 runtime。runtime 加载驱动、校准 UCM、启动语音桥，验证后原子发布 `ready`。
5. 媒体：broker 回应 `READY`，HiDeck 打开真实 ALSA，并替换响铃阶段的静音占位端口。初始化存在约数秒延迟，不能把 WebRTC connected 当作声音已就绪。
6. 结束：HiDeck 先关闭 ALSA，再关闭 socket；broker 停 runtime、关闭 UAC、执行语音路由清理。没有电话时不保持 D4 路由。
7. 异常：准备取消、连接退出、容器崩溃均释放占用。准备失败或 ALSA 无法打开要报错、清理并尝试挂断，不继续伪装成有音频。broker 同时只允许一个通话占用；不支持多模块共用一个 socket 或多路通话混音。

### 服务与安全边界

| 文件 | 职责 |
| --- | --- |
| `hideck-qdc507-audio-broker.service` | 开机启动、监督 broker；待机时应为 active |
| `hideck-qdc507-audio-broker` | root-only Unix socket，按连接生命周期管理路由 |
| `hideck-qdc507-audio.service` | 按需启动的实际音频 runtime；待机时 inactive 是正常状态 |
| `hideck-qdc507-audio-runtime` | ADB 驱动加载、UCM 校准、D4/UAC 建立及 ready 标记 |
| `internal/volte/route_lease.go` | 申请、取消、PCM 包装释放和 broker 断开处理 |

socket 权限为 `0600`，仅适用于当前以 root 运行的 HiDeck 容器。继续只读挂载整个 `/run/hideck-qdc507-audio` 目录；Linux 允许在只读 bind mount 中连接 Unix socket，不需要把整个目录改成可写。不要单独 bind mount socket 文件，否则 broker 重启后可能仍指向旧 inode。

`managed` 是显式启用标记，broker 退出后保留：broker 不可用时必须失败，而不是偷偷回退到常驻模式。本方案不增加 TCP 监听、不需要保存新密码，不暴露 systemctl/ADB 的通用远程执行接口。现有 privileged 容器仍有较大宿主权限，应限制管理网访问。

## 安装与重启自恢复

前提：已有 USB 驱动校正、模块 boot-reset 服务，音频资产已安装，HiDeck 挂载运行目录，部署的是包含 `route_lease.go` 的程序。

在无通话的维护窗口，从仓库根目录执行；路径为通用示例：

```sh
sudo install -m 755 packaging/qdc507/hideck-qdc507-audio-broker /usr/local/sbin/
sudo install -m 644 packaging/qdc507/hideck-qdc507-audio-broker.service /etc/systemd/system/
sudo install -m 644 packaging/qdc507/hideck-qdc507-audio.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl disable --now hideck-qdc507-audio.service
sudo systemctl enable --now hideck-qdc507-audio-broker.service
```

升级 HiDeck 二进制时，保持原容器网络、HTTPS、设备挂载和数据卷不变。**旧容器 entrypoint 等待 ready 或固定网卡名，必须用新版部署脚本重建为只等待 broker socket，否则待机或网卡改名后可能无法启动。** 网卡由设备扫描动态发现。等待 broker socket 出现后启动 HiDeck 并刷新 Edge。不要再运行临时 `call-scoped-audio-experiment.py`。

### USB 热插拔恢复

`hideck-qdc507-hotplug.service` 补充已有 USB 驱动 watcher。旧 watcher 只修正接口归属，不能释放应用中失效的 QMI/AT 句柄或恢复 ADB 发现。新服务仅支持单个 DJI `2ca3:4006`：

1. 每两秒检查 USB 路径、busnum、devnum，要求九个接口和 QMI 网卡完整，驱动归属连续稳定至少六秒。
2. 等待本次主机启动的 boot-reset 完成，避免与固件重启竞争。
3. 核对 ADB USB 路径并读取模块 boot ID；失败时重建主机 ADB server，再核对身份。
4. 停止已运行的 HiDeck 容器后，使用宿主 `qmicli` 独占执行 DMS 查询，避免和应用 QMI client 竞争。若仍超时，核对 ADB 身份后尝试一次模块软件重启，验证 boot ID 变化并等待驱动、ADB 恢复，再重新查询 QMI。
5. 重启音频 broker、重新启动 HiDeck，丢弃上一代设备的句柄。失败或收到正常停止信号也尝试重新启动原来运行的容器。保留音频 socket 目录、数据库及网络/电话策略。
6. QMI 查询及 HTTP ping 成功后在 `/run` 记录已处理的 USB 实例；同一实例不重复恢复。失败退避 60 秒再尝试，模块变化则重新等待稳定。接口级重新授权可能不改变 devnum，因此也识别接口消失后重新出现。

不主动启动管理员事先手动停止的容器，不修改防火墙，不自动拨号，也不通过反复重启模块掩盖呼叫失败。模块重启前创建 `/run/hideck-qdc507-hotplug-reset-attempted`；未完整恢复时保留标记，后续尝试不得再次重启模块，包括 supervisor 自身重启后。完整恢复后才清除预算标记。失败后应查看日志，不要无条件删除该标记循环重试。ADB server 重启影响该主机所有 ADB 客户端，因此此服务用于单模块专用主机，宿主需安装 `qmicli`、`adb`、`curl`。QMI 查询和 `ping` 成功不代表 SIM 驻网、短信、数据出口或真人双向通话已经验收。

```sh
sudo install -m 755 packaging/qdc507/hideck-qdc507-hotplug /usr/local/sbin/
sudo install -m 644 packaging/qdc507/hideck-qdc507-hotplug.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now hideck-qdc507-hotplug.service
```

必须先用新版 `deploy-qdc507-host.sh` 重建容器以移除固定网卡名等待条件，保留旧容器但关闭其自动重启以便回滚。初次启用 hotplug 服务会执行一次恢复；应在无通话维护窗口安装。验收应分别记录软件 USB 重新枚举、真实物理插拔、整机重启和真人通话，不能相互替代。

当前恢复服务以本次主机 boot-reset 已成功为前提。若主机启动时未插模块导致 boot-reset 超时，之后首次插入还需管理员启动 boot-reset；这个场景尚未实现完整无人干预恢复。完整 USB 断开会打断当前通话，不保证恢复原通话。维护期间需要手动停止容器时，应先停止 hotplug 服务，避免与正在进行的恢复竞争。

2026-09-23：新增恢复测试九项及原 broker 四项通过。第一轮软件取消授权五秒后重新授权，暴露了“容器 healthy 但 QMI 超时”的不足；随后加入独占 QMI 查询、一次模块重启兜底及重枚举后的 ADB 发现重试。真实物理重新插拔仍待再次验收。部分外呼返回 `NO CARRIER` 后用户确认目标手机有未接记录，不能将其直接归因为拨号未送达；随后重拨成功接听并完成下述延迟听测。撤回此功能时先 `systemctl disable --now hideck-qdc507-hotplug.service`；保留原 USB watcher、boot-reset 和 audio broker。

修订后的第二轮软件断开测试完全未人工介入恢复：supervisor 检测 QMI 超时后自行重启模块一次，验证新 USB 实例和 boot ID，再恢复服务。约 35 秒完成日志中的恢复流程（不含先前枚举等待），设备 API 显示控制在线、蜂窝已注册、电话 ready/registered；独立音频租约测试返回 READY。该结果不替代实际外呼/呼入听测。

重启后的设计流程：USB watch 修正接口绑定 → boot-reset 对已核实身份的模块做一次软件重启 → broker 启动并创建 managed/socket → HiDeck 自动恢复 QMI、短信与 AT 监听 → 来电/外呼进入活动态后按需准备音频。broker 待机不需要 ready 文件；ready 只表示当前路由已启用。

`/run` 是易失目录。必须保证容器挂载的是宿主同一个目录，不要在容器运行时删除再重建它；broker 在启动时创建目录，部署脚本确认 socket 存在后才创建容器。若 USB/ADB 完全失联，服务会报错，不保证免除物理维修。本版本已完成下述温重启真人验收；迁移主机、固件或启动配置后仍需重新验收。

## 验证与排错

### 接通后的音频准备延迟优化（2026-09-23）

保持“通话 active 后才申请路由”，不提前占用 D4/UAC。移除设备节点检查通过后固定的两秒等待，将五条 UCM 命令的间隔从 0.5 秒缩短为 0.1 秒；仍检查 ACDB 初始化、VocProc 校准、bridge 检查、路由 active 和 audio_enable。runtime 输出阶段耗时，broker 输出清理及完整准备耗时。

同一现场待机租约基线一次 6.779 秒；调整后三次为 2.874、2.367、2.368 秒，均返回 READY。数据仅代表申请路由至 READY，不包括 AT 状态确认、ALSA 打开、浏览器缓冲和真人听感。

随后真实 WebUI 外呼接通时，通过后台通话状态检测（约 150 毫秒查询间隔）立即触发浏览器所在电脑的 `say`，而非等路由 READY 才播放。该通话 broker 记录准备时间 2.372 秒，用户确认接通后约 3 秒开始听到测试语音，体验改善；挂断后路由正常释放。`say` 是现场测试手段，不是生产服务的一部分。该轮仅确认外呼电脑到手机的出声体验，不等于优化后的全部双向音频、呼入、冷启动和真实热插拔首通已验收。

本地资产路径用 systemd drop-in 配置 `QDC507_RUNTIME_DIR`，不要把私有路径写进公共脚本。默认路径是 `/opt/qdc507-voice-runtime`，部署前必须验证目录与资产存在。若固件需要保守节奏，可在 `hideck-qdc507-audio.service` 的 `[Service]` drop-in 设置以下值并 daemon-reload，下一次路由启动生效：

```ini
Environment=QDC507_DEVICE_SETTLE_SECONDS=2
Environment=QDC507_UCM_COMMAND_GAP_SECONDS=0.5
```

优化默认值分别为 `0` 和 `0.1`。不要仅凭 READY 判定音频质量；出现静音或异常应恢复保守配置并重新听测。

### 固化部署检查记录（2026-09-20）

- 前端 314 项测试通过，TypeScript/Vite 构建通过。
- `internal/volte`、`internal/device`、`internal/modem` 测试通过；VoLTE race 检查通过。
- broker 的占用互斥、释放、准备中取消、启动失败和服务退出路径有测试覆盖。
- 已部署 broker 与新 HiDeck 二进制；容器健康，HTTPS 返回 200（命令行连通检查跳过证书校验，不替代浏览器信任检查）。
- 容器内部经只读挂载连接 socket，获得 READY 后释放，broker 正常关闭 D4/UAC，runtime 回到 inactive。
- 待机无 ready 文件时重启容器，HiDeck 健康恢复并重新启动 AT 监听。
- 重启 broker 后，同一已运行容器再次成功连接新 socket。
- 没有删除数据卷或数据库；保留了旧程序和停止的旧容器用于回滚。未改变防火墙和 Wi-Fi。
- Mac 解锁后刷新 Edge，正式部署版本的呼入与外呼均完成手机端真人听测，音频路由由正式 broker 自动准备。
- 新分支推送后再次外呼、再呼入，用户均确认测试语音清楚。分支构建程序与现场部署程序 SHA-256 一致。

### 两轮整机温重启验收（2026-09-20）

| 验证项 | 第一轮 | 第二轮 |
| --- | --- | --- |
| 主机确实重启 | boot ID 变化 | boot ID 再次变化 |
| 模块自动恢复 | boot-reset 自动验证模块 boot ID 变化 | 同样通过 |
| 后端恢复 | broker active、HiDeck healthy、HTTPS 200、AT 监听启动 | 同样通过 |
| QMI 数据会话 | 自动建立并分配地址 | 自动建立并分配地址 |
| 外呼声音 | 接通后播放测试语音，用户确认清楚 | 接通后播放测试语音，用户确认清楚 |
| 呼入声音 | 本轮未单独测试 | 接听后播放测试语音，用户确认清楚 |
| 人工服务修复/USB 插拔 | 无 | 无 |

两轮均在模块保持 USB 供电的条件下执行系统重启，不属于断电冷启动。旧 Edge 页面刷新后恢复正确的设备状态，这一客户端操作必须与后端无人干预恢复区分。每轮曾有未接通的外呼，用户分别说明是手机免打扰和错过接听；准备好后重拨通过，不能把页面响铃状态当作手机已响铃的证据。

验收程序 SHA-256：`5ce9ac379367d2a404e82faa10a3be143d9da3fbb08a1f8664af42a90a234460`。本轮未重新测试短信收发、指定蜂窝出口访问、反向语音内容、长时间压力或冷启动；不把两个样本外推成无限期稳定保证。

```sh
systemctl is-active hideck-qdc507-audio-broker.service
systemctl is-active hideck-qdc507-audio.service
journalctl -u hideck-qdc507-audio-broker.service -n 60 --no-pager
docker logs --since 5m hideck
```

验收至少包括：连续三次呼入；接听到音频就绪的时间；双方读不同短句并核验内容；本地挂断、远端挂断、响铃取消、路由初始化中挂断；外呼；容器重启、broker 重启、整机重启；上网与短信回归。只读峰值不能证明语音内容正确，录音应在用户授权下进行且不能直接提交公共仓库。

注意事项：

- 接听时限是呼入侧设定的 20 秒；GUI 工具延迟不确定，不能以两次成功保证今后都在时限内。优先接听，之后查日志。需要可靠无人值守接听时，应另实现用户明确开启、限定号码的事件驱动自动接听，不能默认接听所有来电。
- Edge 锁屏/休眠后的事件流需要重连；已加入恢复焦点/网络/可见性重连及 45 秒无心跳超时。45 秒机制不是满足 20 秒接听期限的保证。
- HTTPS 信任和 UDP 7580 可达是必要条件，但无法修复模块内部 D4 路由。不要用关防火墙作为长期修复。
- 单个 AT 管理器统一调度；不要并行运行 socat/minicom 等程序抢走 URC。
- `ATA` 的终止 `OK` 会被 AT 管理器消费；以调用错误结果判断成功，不能要求 payload 再包含 OK。
- `CLCC mode=1` 是数据，不是语音；QDC507 的 QMI VOICE 编号不能与 CLCC 编号混用。
- 无声音时先确认实际打开 ALSA，再看样本；响铃占位产生的零 PCM 不是模块音频。
- 路由 ready 仍不等于语音内容正确；目前没有基于语音内容的自动自愈。一次失败不应触发无限重启整个模块。

## 回滚

维护窗口内先停止 broker，再恢复备份的 HiDeck 二进制和原音频 service。把 `managed` 标记移到备份路径，启用原 resident service，重启 HiDeck。保留数据库、证书、HTTPS 与网络配置。常驻版本仍有本次发现的路由时序风险；回滚不代表恢复了稳定音频。
