# The release upload endpoint: sshd and rsync, one user, and nothing of the
# host but the download directory (mounted at /download).
FROM alpine:3.22

# UID of the owner of the download directory on the host, so the files the
# release workflow writes belong to that user and nginx can serve them.
ARG UPLOAD_UID=1000

RUN apk add --no-cache openssh-server rsync coreutils \
 && adduser -D -u "${UPLOAD_UID}" -h /download -s /bin/sh upload \
 # "*": no password, but not locked, so key logins are allowed.
 && sed -i 's/^upload:!:/upload:*:/' /etc/shadow \
 && mkdir -p /etc/ssh/keys

COPY sshd_config /etc/ssh/sshd_config
COPY entrypoint.sh /entrypoint.sh

EXPOSE 2222
ENTRYPOINT ["/bin/sh", "/entrypoint.sh"]
