# crfs-plugin

A [fuse-overlayfs](https://github.com/containers/fuse-overlayfs/)
plugin for using [CRFS](https://github.com/google/crfs) for loading
lower layers.

This is still experimental, use at your own risk.

# Build

You need to have both the Go toolchain and the C compiler installed.
Use `make` from the upper directory for building `crfs-plugin`:

```
$ make
```

If everything worked correctly and all the needed dependencies were
present, the build generates a single `crfs-plugin.so` artifact that
can be directly used from fuse-overlayfs.

You need to use fuse-overlayfs with plugins support from:

https://github.com/containers/fuse-overlayfs/pull/119

# Build a CRFS image

Before using `crfs-plugin`, you need to have a CRFS image.

To create one, you can use:

[stargzify](https://github.com/google/crfs/blob/master/stargz/stargzify/stargzify.go).

You can install it with:

```
$ go get -u github.com/google/crfs/stargz/stargzify
```

Once stargzify is installed, you can convert a image to the .stargz
format with:
```
$ stargzify docker.io/fedora localhost:5000/myimages/fedora:stargz
```

# Usage

There are a few new options needed to pass to fuse-overlays to
work with `crfs-plugin`:

- `fast_ino=1`: force fuse-overlayfs to stop looking for an inode
  number after the first one found during the lookup.
- `plugins=/path/a.so:/path2/b.so`: list of plugins that
  fuse-overlayfs loads and can be used to handle lower layers.
- `lowerdir=//$PLUGIN/$CONF/lowerdirpath`: when a lowerdir path starts
  with `//`, fuse-overlay uses the plugin `$PLUGIN` to manage
  `lowerdir` with the specified configuration `$CONF`, that is
  specific to the plugin.
  For `crfs-plugin`, the configuration is the image/layer to use in
  base64 encoding.

Accepted configuration strings are:

- https://www.example.com/layer.stargz: use directly the layer at the URL.
- docker://docker.io/image:stargz: use the image at the specified registry.
- /path/to/the/file.stargz: use the file as a .stargz layer.

The easiest way run fuse-overlayfs is from a user namespace (or if you
like danger, directly as root user).

When an image `docker://` is used, `fuse-overlayfs` will internally
create more lower layers depending on how many there are present in
the image.

`podman unshare` can be helpful to create a user+mount namespace that
can be used by an unprivileged user.

```
$ podman unshare
# mkdir lower upper workdir merged
# fuse-overlayfs -o fast_ino=1,plugins=/path/to/crfs-plugin.so,lowerdir=//crfs/$(echo -n docker://localhost:5000/myimages/fedora:stargz | base64 -w0)/$(pwd)/lower,upperdir=upper,workdir=work merged
# podman run --rm -ti --rootfs $(pwd)/merged echo hello
```

It is possible to use directly a URL for retrieving a `.stargz`
layer.  When the lowerdir location starts with the `https://`
prefix, then the file at that URL will be used directly.

```
# fuse-overlayfs -o fast_ino=1,plugins=/path/to/crfs-plugin.so,lowerdir=//crfs/$(echo -n https://url/to/stargzified-layer.tar.gz | base64 -w0)/$(pwd)/lower,upperdir=upper,workdir=work merged
```

Alternatively, it is possible to use a local file:

```
# fuse-overlayfs -o fast_ino=1,plugins=/path/to/crfs-plugin.so,lowerdir=//crfs/$(echo -n /path/to/stargzified-layer.tar.gz | base64 -w0)/$(pwd)/lower,upperdir=upper,workdir=work merged
```
