# Unified HiDeck deployment

The production root is `/opt/docker/hideck`. Source/build worktrees are separate.

| Directory | Contents |
| --- | --- |
| `bin/hideck` | Deployed application binary |
| `config/`, `data/`, `logs/` | Private configuration/certificates, persistent data, logs |
| `assets/qdc507-voice-runtime/` | Separately provisioned, checksum-verified module voice assets |
| `host/bin/` | USB, boot reset, hotplug and audio scripts |
| `host/systemd/` | Service units and site-specific drop-ins |
| `host/udev/` | USB binding and ModemManager exclusion rules |
| `docs/` | Published architecture/runbook copies |
| `archive/` | Previous installers, binaries and migration backups |

`/usr/local/sbin`, `/etc/systemd/system` and `/etc/udev/rules.d` retain symlink entry points into `host/`. Unit templates deliberately use those stable system entry points. `/run/hideck-qdc507-*` remains volatile runtime state: never move or recreate a mounted socket directory. Docker engine storage and system packages (adb, qmicli, Python, curl) remain system-managed shared dependencies.

## Install or update host files

From the repository, in an idle maintenance window:

```sh
sudo bash packaging/qdc507/install-host.sh
```

The installer saves replaced files/links to a unique `archive/host-install.*` directory, installs scripts/units/rules/docs, reloads systemd and udev, and **does not restart/enable services, move application data, install voice binaries, or create containers**. Existing site-specific drop-ins are preserved. `DESTDIR=/absolute/staging/path` installs a test/package tree without touching live services; its absolute symlinks intentionally target the final deployment paths.

Keep the deployment root private (0700); credentials, certificate keys and backups must not be published. Supply the application binary as `bin/hideck`, the application configuration in `config/`, and licensed voice assets in `assets/qdc507-voice-runtime/`. The runtime retains its checksum checks. Override `QDC507_RUNTIME_DIR` only via a site-specific service drop-in when necessary.

For a new, fully provisioned installation, review the runbook prerequisites, then:

```sh
sudo systemctl enable --now hideck-qdc507-usb.service
sudo systemctl enable --now hideck-qdc507-boot-reset.service
sudo systemctl disable --now hideck-qdc507-audio.service
sudo systemctl enable --now hideck-qdc507-audio-broker.service
sudo /opt/docker/hideck/deploy-qdc507-host.sh
sudo systemctl enable --now hideck-qdc507-hotplug.service
```

`deploy-qdc507-host.sh` refuses to replace an existing `hideck` container. It preserves the host-network/privileged design and mounts the new root's binary, configuration, data and logs. `HIDECK_DIR` overrides container storage only; it does not relocate the host installation or voice assets.

## Upgrade an old layout

Do not run the new deploy script against an empty configuration directory. First check for active calls, inventory actual mounts and site overrides, stop the hotplug supervisor and container, then stop the broker/runtime and USB watcher. Back up the stopped database together with its configuration and container configuration. Move the deployment data and voice assets into the structure above, preserving permissions; archive historical binaries and scripts rather than deleting them.

Move site-specific systemd drop-in files into `host/systemd/<unit>.d/` and leave a symlink from `/etc/systemd/system/<unit>.d`. Update old asset paths to the new `assets/` location. The installer deliberately does not guess or overwrite these private settings. Keep temporary compatibility symlinks at old deployment/asset paths when needed for rollback.

Disable the old container's restart policy, stop it and rename it to a unique backup name. After installing the new host files and restarting the USB watcher/broker, create the new container with `deploy-qdc507-host.sh`, then resume the hotplug supervisor. Do not run old and new containers concurrently: they share hardware and may share the database.

For rollback, stop the supervisor and new container before restoring the previous container name/restart policy and service files. Do not restore a database snapshot over newer data without a fresh backup. Historical containers and compatibility links are retained intentionally, not a second active deployment.

## Verification and limits

Check actual container mount sources, HTTPS, device control, cellular registration, phone readiness and an audio lease with no active call. Confirm lease cleanup and enabled recovery services. A successful ping alone does not establish QMI health. Verify real calls and host reboot separately.

The directory migration preserved the application binary and network settings. On-site verification found HTTPS working, control online, cellular registered, VoLTE ready, and an audio lease READY in 2.669 seconds with normal release. This migration did not include a new live call, SMS roundtrip, physical USB replug or host reboot.

See [architecture](docs/hideck-qc507.md) and [audio/recovery runbook](docs/packaging/qdc507/CALL-SCOPED-AUDIO.md) in the installed tree.
