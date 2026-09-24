#!/usr/bin/env bash
# Install files only: never restart services, recreate containers or move data.
set -euo pipefail
source_dir=$(cd "$(dirname "$0")" && pwd)
repo_dir=$(cd "$source_dir/../.." && pwd)
root=/opt/docker/hideck
# Packaging/test staging only. Links retain their final absolute targets.
stage=${DESTDIR:-}
[[ -z "$stage" || "$stage" == /* ]] || { echo 'DESTDIR must be absolute' >&2; exit 1; }
if [[ -z "$stage" && "$EUID" != 0 ]]; then
  echo 'Run as root, or set DESTDIR for a staged installation.' >&2
  exit 1
fi
install -d -m 700 "$stage$root"
install -d "$stage$root/host/bin" "$stage$root/host/systemd" \
  "$stage$root/host/udev" "$stage$root/docs/packaging/qdc507" "$stage$root/archive" \
  "$stage/usr/local/sbin" "$stage/etc/systemd/system" "$stage/etc/udev/rules.d"
backup=$(mktemp -d "$stage$root/archive/host-install.XXXXXX")
preserve() {
  local path=$1
  if [[ -e "$stage$path" || -L "$stage$path" ]]; then
    [[ ! -d "$stage$path" ]] || { echo "Refusing directory replacement: $path" >&2; exit 1; }
    mkdir -p "$backup${path%/*}"
    cp -a "$stage$path" "$backup$path"
  fi
}
put() {
  preserve "$2"
  install -m "$3" "$1" "$stage$2"
}
link() {
  preserve "$2"
  ln -sfn "$1" "$stage$2"
}
for name in hideck-qdc507-usb-watch qdc507-usb-bind.sh hideck-qdc507-boot-reset \
  hideck-qdc507-hotplug hideck-qdc507-audio-broker hideck-qdc507-audio-runtime; do
  put "$source_dir/$name" "$root/host/bin/$name" 755
  link "$root/host/bin/$name" "/usr/local/sbin/$name"
done
for name in hideck-qdc507-usb.service hideck-qdc507-boot-reset.service \
  hideck-qdc507-hotplug.service hideck-qdc507-audio-broker.service hideck-qdc507-audio.service; do
  put "$source_dir/$name" "$root/host/systemd/$name" 644
  link "$root/host/systemd/$name" "/etc/systemd/system/$name"
done
for name in 99-qdc507.rules 78-hideck-qdc507-ignore.rules; do
  put "$source_dir/$name" "$root/host/udev/$name" 644
  link "$root/host/udev/$name" "/etc/udev/rules.d/$name"
done
put "$repo_dir/deploy-qdc507-host.sh" "$root/deploy-qdc507-host.sh" 755
put "$repo_dir/hideck-qc507.md" "$root/docs/hideck-qc507.md" 644
put "$source_dir/CALL-SCOPED-AUDIO.md" "$root/docs/packaging/qdc507/CALL-SCOPED-AUDIO.md" 644
put "$source_dir/DEPLOYMENT.md" "$root/docs/packaging/qdc507/DEPLOYMENT.md" 644
put "$source_dir/DEPLOYMENT.md" "$root/README.md" 644
if [[ -z "$stage" ]]; then
  systemctl daemon-reload
  udevadm control --reload-rules
fi
printf 'Files installed under %s; previous files retained in %s\n' "$root" "$backup"
echo 'No services restarted or enabled; no containers or application data changed.'
