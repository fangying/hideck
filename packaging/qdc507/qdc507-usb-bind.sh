#!/usr/bin/env bash
# Quectel 系 QMI 模组（DJI Baiwang QDC507 2ca3:4006 / EC25 2c7c:0125 等）驱动自动绑定。
# 设计约束（不写死）：
#   - VID/PID 从 sysfs 现读（换模组、改 ID 都通用）
#   - QMI 接口号动态识别：vendor 类 ff/ff/ff 且 >=3 端点者中取编号最大者（Quectel 布局 QMI 恒为最后一个；
#     DIAG 同为 ff/ff/ff 但只有 2 端点，自动排除）；找不到则停止
#   - 若内核已自动绑定（如标准 2c7c:0125），脚本幂等空跑
set -u
exec 9>/run/qdc507-usb-bind.lock
flock -x 9

dev="$1"                                   # udev %k，如 5-1
base="/sys/bus/usb/devices/$dev"
log=/run/qdc507-usb-bind.log

logmsg() { printf '%s %s\n' "$(date '+%F %T')" "$*" >> "$log"; }
logmsg "== bind $dev start =="

vid=$(cat "$base/idVendor" 2>/dev/null || echo "")
pid=$(cat "$base/idProduct" 2>/dev/null || echo "")
[ -n "$vid" ] && [ -n "$pid" ] || { logmsg "no vid/pid for $dev"; exit 1; }
case "$vid:$pid" in
  2ca3:4006|2c7c:0125) ;;
  *) logmsg "unsupported device $vid:$pid"; exit 1 ;;
esac

# 1) option 串口驱动认领 DJI composite gadget 的 DIAG/serial 接口。
modprobe option || exit 1
# 动态 ID 泛匹配会抢占其他接口；下面校正绑定，watcher 覆盖重新枚举。
if [ -d /sys/bus/usb-serial/drivers/option1 ]; then
  echo "$vid $pid" > /sys/bus/usb-serial/drivers/option1/new_id 2>/dev/null
fi

# 2) 等待接口目录出现
for _ in $(seq 1 100); do
  ls -d "$base"/"$dev":1.* >/dev/null 2>&1 && break
  sleep 0.1
done

# 3) 动态识别 QMI 接口
qmi_if=""
last_if=""
for iface in "$base"/"$dev":1.*; do
  [ -d "$iface" ] || continue
  name=${iface##*/}
  last_if=$name
  cls=$(cat "$iface/bInterfaceClass" 2>/dev/null)
  sub=$(cat "$iface/bInterfaceSubClass" 2>/dev/null)
  proto=$(cat "$iface/bInterfaceProtocol" 2>/dev/null)
  eps=$(ls -d "$iface"/ep_* 2>/dev/null | wc -l)
  if [ "$cls" = "ff" ] && [ "$sub" = "ff" ] && [ "$proto" = "ff" ] && [ "$eps" -ge 3 ]; then
    qmi_if=$name        # 取最后一个匹配（编号最大）
  fi
done
[ -n "$qmi_if" ] || { logmsg "no QMI interface found"; exit 1; }
logmsg "vid/pid=$vid/$pid qmi_if=$qmi_if"

# 4) 移交 QMI 接口给 qmi_wwan（若已被 option 抢占）
if [ -L "$base/$qmi_if/driver" ]; then
  drv=$(readlink "$base/$qmi_if/driver"); drv=${drv##*/}
  if [ "$drv" = "option" ]; then
    echo "$qmi_if" > /sys/bus/usb/drivers/option/unbind 2>/dev/null
    logmsg "unbound $qmi_if from option"
  fi
fi
if [ ! -L "$base/$qmi_if/driver" ]; then
  modprobe qmi_wwan 2>/dev/null || true
  echo "$vid $pid" > /sys/bus/usb/drivers/qmi_wwan/new_id 2>/dev/null
  echo "$qmi_if" > /sys/bus/usb/drivers/qmi_wwan/bind 2>/dev/null
  sleep 1
  if [ -L "$base/$qmi_if/driver" ] && readlink "$base/$qmi_if/driver" | grep -q qmi_wwan; then
    logmsg "bound $qmi_if -> qmi_wwan"
  else
    # 移交失败则恢复给 option，保证串口不丢
    echo "$qmi_if" > /sys/bus/usb-serial/drivers/option1/bind 2>/dev/null
    logmsg "WARN: qmi_wwan bind failed, restored $qmi_if to option"
  fi
else
  logmsg "$qmi_if already on $(readlink "$base/$qmi_if/driver" | xargs basename) (no-op)"
fi

# 5) qmi_wwan 会按 ff/ff/ff 认领 serial transports 1-3。它们不是多余 QMI
#    通道：QDC507 的 USB gadget 声明为 diag,serial,rmnet,ffs,audio，接口 1-3
#    必须交给 option，接口 2/3 均已实测响应 AT。只从 qmi_wwan 解绑；若 option
#    已接管则保留。
for iface in "$base"/"$dev":1.*; do
  [ -d "$iface" ] || continue
  name=${iface##*/}
  [ "$name" = "$qmi_if" ] && continue
  cls=$(cat "$iface/bInterfaceClass" 2>/dev/null)
  sub=$(cat "$iface/bInterfaceSubClass" 2>/dev/null)
  if [ "$cls" = "ff" ] && [ "$sub" != "42" ] && [ "$name" != "$dev:1.0" ]; then
    if [ -L "$iface/driver" ] && readlink "$iface/driver" | grep -q qmi_wwan; then
      echo "$name" > /sys/bus/usb/drivers/qmi_wwan/unbind 2>/dev/null
      logmsg "unbound $name from qmi_wwan (serial transport)"
      echo "$name" > /sys/bus/usb/drivers/option/bind 2>/dev/null || true
      if [ -L "$iface/driver" ] && readlink "$iface/driver" | grep -q '/option$'; then
        logmsg "bound $name -> option (serial transport)"
      fi
    fi
  fi
done

# 6) 释放 ADB 与音频接口（若被 option 的 new_id 泛匹配误抢），保证 adb 可连接、
#    UAC 声卡可被 snd-usb-audio 认领。
#    竞态说明：接口 probe 是异步的，option 可能在主流程执行后才认领 ADB/音频接口，
#    因此轮询重试直到稳定：先解绑全部被 option 抢的 ADB/Audio，再绑 AC（01/01），
#    最后校验 ADB 无驱动占用、全部 Audio 接口归 snd-usb-audio。
audio_ac=
modprobe snd-usb-audio || exit 1
for _ in $(seq 1 12); do
  # 6a) 解绑 option 抢的 ADB/音频接口
  for iface in "$base"/"$dev":1.*; do
    [ -d "$iface" ] || continue
    name=${iface##*/}
    cls=$(cat "$iface/bInterfaceClass" 2>/dev/null)
    sub=$(cat "$iface/bInterfaceSubClass" 2>/dev/null)
    [ -L "$iface/driver" ] || continue
    drv=$(readlink "$iface/driver"); drv=${drv##*/}
    [ "$drv" = "option" ] || continue
    if { [ "$cls" = "ff" ] && [ "$sub" = "42" ]; } || [ "$cls" = "01" ]; then
      echo "$name" > /sys/bus/usb/drivers/option/unbind 2>/dev/null
      logmsg "unbound $name (ADB/Audio) from option"
    fi
  done
  # 6b) AC 接口空闲时交给 snd-usb-audio（AS 由驱动随 AC 接管）
  if [ -z "$audio_ac" ]; then
    for iface in "$base"/"$dev":1.*; do
      [ -d "$iface" ] || continue
      name=${iface##*/}
      cls=$(cat "$iface/bInterfaceClass" 2>/dev/null)
      sub=$(cat "$iface/bInterfaceSubClass" 2>/dev/null)
      [ "$cls" = "01" ] && [ "$sub" = "01" ] || continue
      if [ ! -L "$iface/driver" ]; then
        echo "$name" > /sys/bus/usb/drivers/snd-usb-audio/bind 2>/dev/null
        if [ -L "$iface/driver" ] && readlink "$iface/driver" | grep -q snd-usb-audio; then
          logmsg "bound $name -> snd-usb-audio"
          audio_ac=$name
        else
          logmsg "WARN: snd-usb-audio bind failed for $name"
        fi
      elif readlink "$iface/driver" | grep -q snd-usb-audio; then
        audio_ac=$name
      fi
      break
    done
  fi
  # 6c) 校验：ADB 无驱动占用，Audio 接口不再有 option 占用，AC 已归 snd
  ok=1
  for iface in "$base"/"$dev":1.*; do
    [ -d "$iface" ] || continue
    cls=$(cat "$iface/bInterfaceClass" 2>/dev/null)
    sub=$(cat "$iface/bInterfaceSubClass" 2>/dev/null)
    if [ "$cls" = "ff" ] && [ "$sub" = "42" ]; then
      if [ -L "$iface/driver" ]; then
        drv=$(basename "$(readlink "$iface/driver")")
        [ "$drv" = usbfs ] || ok=0
      fi
    elif [ "$cls" = "01" ]; then
      if [ -L "$iface/driver" ] && readlink "$iface/driver" | grep -q option; then
        ok=0
      fi
    fi
  done
  [ -n "$audio_ac" ] || ok=0
  [ "$ok" = "1" ] && break
  sleep 1
done
logmsg "== bind $dev done =="
