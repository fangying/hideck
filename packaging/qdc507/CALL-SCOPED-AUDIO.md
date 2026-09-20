# QDC507 按通话生命周期管理音频

## 结论与验收边界

本轮连续两次外部呼入，接听后从浏览器所在 Mac 扬声器播放测试语音，手机端均确认听到。第二次从后台首次 RING 到通话 active 约 17 秒。模块返回的真实 PCM 不再全零，出现随讲话变化的幅值；本轮没有独立录音转写验证手机到浏览器的语音内容，不能仅凭峰值宣布双向长期稳定。

这两次真人测试先使用临时脚本验证生命周期。随后部署正式 Unix socket broker，已分别完成一次外部呼入接听和一次主动外呼，用户在两次通话中均确认手机端听到了接听侧播放的语音。此次不再依赖临时日志监听脚本。主机重启、冷启动、短信和上网仍不能沿用旧版本的验收结论；手机到电脑的语音内容及长时间稳定性仍需单独核验。

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

升级 HiDeck 二进制时，保持原容器网络、HTTPS、设备挂载和数据卷不变。**旧容器 entrypoint 等待 ready，必须用新版部署脚本重建为等待 broker socket，否则待机会启动死锁。** 新版脚本仍需按目标机核实 QMI 网卡名称。等待 broker socket 出现后启动 HiDeck 并刷新 Edge。不要再运行临时 `call-scoped-audio-experiment.py`。

重启后的设计流程：USB watch 修正接口绑定 → boot-reset 对已核实身份的模块做一次软件重启 → broker 启动并创建 managed/socket → HiDeck 自动恢复 QMI、短信与 AT 监听 → 来电/外呼进入活动态后按需准备音频。broker 待机不需要 ready 文件；ready 只表示当前路由已启用。

`/run` 是易失目录。必须保证容器挂载的是宿主同一个目录，不要在容器运行时删除再重建它；现有部署脚本应在启动容器前创建该目录。若 USB/ADB 完全失联，服务会报错，不保证免除物理维修。本次固化后必须另做主机重启真人验收，不能引用此前常驻版的成功来代替。

## 验证与排错

### 固化部署检查记录（2026-09-20）

- 前端 314 项测试通过，TypeScript/Vite 构建通过。
- `internal/volte`、`internal/device`、`internal/modem` 测试通过；VoLTE race 检查通过。
- broker 的占用互斥、释放、准备中取消、启动失败和服务退出路径有测试覆盖。
- 已部署 broker 与新 HiDeck 二进制；容器健康，HTTPS 返回 200（命令行连通检查跳过证书校验，不替代浏览器信任检查）。
- 容器内部经只读挂载连接 socket，获得 READY 后释放，broker 正常关闭 D4/UAC，runtime 回到 inactive。
- 待机无 ready 文件时重启容器，HiDeck 健康恢复并重新启动 AT 监听。
- 重启 broker 后，同一已运行容器再次成功连接新 socket。
- 没有删除数据卷或数据库；保留了旧程序和停止的旧容器用于回滚。未改变防火墙和 Wi-Fi。
- Mac 解锁后刷新 Edge，正式部署版本的呼入与外呼均完成手机端真人听测。音频路由由正式 broker 自动准备；整机重启未在本轮固化中执行。

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
