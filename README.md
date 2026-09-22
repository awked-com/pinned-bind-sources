# Pinned bind sources

A Linux command that holds directory file descriptors while creating private bind
mounts. It rejects symlink and dot-component traversal, so replacing a source path
after it is opened does not redirect the bind mount.

## Install

With Go 1.25.8 or newer on Linux:

```sh
go install github.com/awked-com/pinned-bind-sources@latest
```

## Use

Run with mount privileges (normally root). The runtime directory must already
exist, be owned by the invoking user, and have no group or other permissions.
Use a dedicated mount namespace if the mounts should remain local to a service.

```sh
install -d -m 0700 /run/example-bind
pinned-bind-sources pin bindings.json /run/example-bind
# Consume /run/example-bind/0 while the service runs.
pinned-bind-sources unpin bindings.json /run/example-bind
```

Example `bindings.json`:

```json
[{"target":"0","path":"/srv/example","create":true,"uid":1000,"gid":1000,"mode":"0750"}]
```

Targets must be unique numeric directory names. Paths must be absolute. `create`
defaults to false; omitted `uid`, `gid`, and `mode` leave existing metadata
unchanged. Creating a source also creates missing ancestors. Metadata changes
apply to the source.

Use a trusted manifest and private runtime directory. Each `pin` first clears old
mounts in that directory. A failed pin can leave earlier mounts in place; call
`unpin` during service cleanup. `unpin` detaches mounts and removes the runtime
directory, and succeeds if that directory is already absent. Keep the manifest
available until cleanup finishes.

## Development

`go test ./...` runs the unprivileged tests on Linux. To exercise real mounts:

```sh
go test -c -o /tmp/pinned-bind-sources.test
sudo unshare --mount --propagation private env PINNED_BIND_MOUNT_TESTS=1 /tmp/pinned-bind-sources.test
```

CI runs both test groups on x86_64 and aarch64 Linux. Version tags use `vX.Y.Z`.
