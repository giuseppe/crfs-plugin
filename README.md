# crfs-plugin

A [fuse-overlayfs](https://github.com/containers/fuse-overlayfs/)
plugin for using [CRFS](https://github.com/google/crfs) for loading
lower layers.

This is still experimental, use at your own risk.

# Build

You need to have both the Go toolchain and the C compiler installed.

``make``

The build generates a single `crfs-plugin.so` artifact that can be used
from fuse-overlayfs.

You fuse-overlayfs with plugins support from:
https://github.com/containers/fuse-overlayfs/pull/119


# Usage

Before you need to create a stargz image using
[stargzify](https://github.com/google/crfs/blob/master/stargz/stargzify/stargzify.go).

The easiest is to run fuse-overlayfs from within a user namespace (or
if you like danger, as root).

`podman unshare` can be helpful to create a user+mount namespace that
can be used by an unprivileged user.

```
$ podman unshare
# mkdir lower upper workdir merged
# fuse-overlayfs -o fast_ino=1,plugins=/path/to/crfs-plugin.so,lowerdir=//test/$(echo -n https://url/to/stargzified-layer.tar.gz base64 -w0)/$(pwd)/lower,upperdir=upper,workdir=work merged
# podman run --rm -ti --rootfs $(pwd)/merged echo hello
```
