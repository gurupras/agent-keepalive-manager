#!/bin/sh
# Keep one host key for the life of the keys volume, so the release
# workflow's known_hosts entry stays valid across restarts and upgrades.
set -eu
key=/etc/ssh/keys/ssh_host_ed25519_key
if [ ! -f "$key" ]; then
  ssh-keygen -q -t ed25519 -N "" -C agent-keepalive-upload -f "$key"
fi
echo "host key: $(ssh-keygen -lf "$key.pub")"
# sshd refuses a key file the user (or a group) could write; the mounted one
# carries the host's modes, so install a root-owned copy.
install -d -m 755 /run/ssh
install -m 644 /etc/ssh/authorized_keys.src /run/ssh/authorized_keys
exec /usr/sbin/sshd -D -e
