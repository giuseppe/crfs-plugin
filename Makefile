crfs-plugin.so: fuse-overlayfs/config.h go/go.a c/crfs-plugin.c
	$(CC) -shared -I fuse-overlayfs \
	fuse-overlayfs/utils.c \
	-I ./go -fPIC \
	c/crfs-plugin.c \
	go/go.a -l pthread \
	 -o $@

fuse-overlayfs:
	git clone --depth=1 -b c-plugins https://github.com/giuseppe/fuse-overlayfs

fuse-overlayfs/config.h: fuse-overlayfs
	(cd fuse-overlayfs; ./autogen.sh; ./configure)
	touch $@

go/go.a: go/crfs-plugin.go
	(cd go; go build -buildmode=c-archive)

clean:
	rm -f crfs-plugin.so go/go.a

.PHONY: clean
