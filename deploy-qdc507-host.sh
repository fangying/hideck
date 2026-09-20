#!/usr/bin/env bash
set -euo pipefail
hideck_dir=${HIDECK_DIR:-/opt/hideck}
cd "$hideck_dir"

test -x "$PWD/bin/hideck"
test -f /run/hideck-qdc507-audio/managed
test -S /run/hideck-qdc507-audio/control.sock
if docker container inspect hideck >/dev/null 2>&1; then
  echo 'hideck exists; remove it explicitly before recreating.'
  exit 1
fi

iface=wwp200s0f3u1i4
raw_ip="/sys/class/net/$iface/qmi/raw_ip"
if [ -w "$raw_ip" ]; then
  printf 'Y' > "$raw_ip"
fi

# Host networking keeps Pion ICE on the host UDP socket. The QMI interface
# remains in the host namespace and is therefore not moved into the container.
# USB device and ALSA card numbers can change after re-enumeration.
# Mount the host /dev so the already-running container
# sees the final udev nodes without requiring another recreation.
cid=$(docker run -d --name hideck --restart unless-stopped --init --stop-timeout 30 \
  --privileged \
  --network host \
  -v /dev:/dev \
  -v /sys/bus/usb:/sys/bus/usb:ro \
  -v /run/hideck-qdc507-audio:/run/hideck-qdc507-audio:ro,z \
  -v "$PWD/bin/hideck:/usr/local/bin/hideck:ro,Z" \
  -v "$PWD/config:/app/config:Z" \
  -v "$PWD/data:/app/data:Z" \
  -v "$PWD/logs:/app/logs:Z" \
  -e TZ=Asia/Shanghai -e CONFIG_PATH=/app/config/config.yaml \
  --log-driver json-file --log-opt max-size=10m --log-opt max-file=3 \
  --entrypoint /bin/sh \
  yibaiba/hideck@sha256:b46adb1b8679cbc884838c99291c679b879c8e3b0dc2e793f4bf1a70f2295663 \
  -c 'until [ -e /sys/class/net/wwp200s0f3u1i4 ] && [ -S /run/hideck-qdc507-audio/control.sock ]; do sleep 1; done; exec /usr/local/bin/hideck -c /app/config/config.yaml')

printf '%s\n' "$cid"
