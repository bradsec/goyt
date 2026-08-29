#!/bin/sh
# Bind-mounted host directories keep their host ownership inside the container,
# so `-v ./downloads:/app/downloads` fails with "[Errno 13] Permission denied"
# whenever the host dir is owned by root (Docker auto-created it) or by a uid
# other than the image's runtime user.
#
# When started as root, take ownership of the writable data dirs, then drop to
# the unprivileged goyt user before running the app. When the caller already
# pinned an unprivileged user (docker run --user ...), skip straight to exec.
set -e

if [ "$(id -u)" = "0" ]; then
    chown goyt:goyt /app /app/downloads /app/assets/yt-dlp 2>/dev/null || true
    exec su-exec goyt:goyt "$@"
fi

exec "$@"
