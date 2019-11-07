PREFIX ?= /usr/local
FUSE_OVERLAYFS ?= fuse-overlayfs

crfs-plugin.so: $(FUSE_OVERLAYFS)/config.h go/go.a c/crfs-plugin.c
	$(CC) -shared -I $(FUSE_OVERLAYFS) \
	$(FUSE_OVERLAYFS)/utils.c \
	-I ./go -fPIC \
	c/crfs-plugin.c \
	go/go.a -l pthread \
	 -o $@

install: crfs-plugin.so
	/usr/bin/install -dD $(PREFIX)/libexec/fuse-overlayfs/
	/usr/bin/install -c crfs-plugin.so -D $(PREFIX)/libexec/fuse-overlayfs/

$(FUSE_OVERLAYFS):
	git clone https://github.com/containers/fuse-overlayfs

$(FUSE_OVERLAYFS)/config.h: $(FUSE_OVERLAYFS)
	(cd $(FUSE_OVERLAYFS); ./autogen.sh; ./configure)
	touch $@

go/go.a: go/crfs-plugin.go
	(cd go; GO111MODULE=on go build -buildmode=c-archive)

clean:
	rm -f crfs-plugin.so go/go.a

.PHONY: clean
