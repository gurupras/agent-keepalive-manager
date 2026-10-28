# Repository signing key (public half)

`agent-keepalive.asc` here is the public key of the GPG key that signs the apt, rpm and arch repositories. The Linux packages ship it and register the repository with it, so it must be the public half of the `REPO_SIGNING_KEY` CI secret; `../publish.sh` refuses to publish when it is not.

It is not committed yet. To create it:

```sh
scripts/gen-repo-key.sh /some/private/dir
cp /some/private/dir/agent-keepalive.asc packaging/repository/keys/
# repo-signing-key.asc -> the REPO_SIGNING_KEY secret; keep an offline copy
```

Until it exists, packages build normally but register no repository. See [docs/package-repositories.md](../../../docs/package-repositories.md).
