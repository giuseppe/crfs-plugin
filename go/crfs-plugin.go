package main

import (
	"C"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/giuseppe/crfs/stargz"
	"github.com/pkg/errors"
)

const chunkSize = int64(1048576 / 2)

const downloadAllFile = false

type layer struct {
	reader  *stargz.Reader
	target  string
	workdir string
	ino     map[string]uint64
}

type urlReaderAt struct {
	contentLength int64
	url           string
	cache         []byte
	cacheRange    string
	client        *http.Client
	cond          *sync.Cond
	destFile      string
	done          bool
	chunks        []bool
	requests      chan int64
	destFileFD    *os.File
}

type dent struct {
	ino  uint64
	name string
	typ  int
}

type dir struct {
	childs []dent
	pos    int
}

var dirs = map[int]*dir{}
var dirHandle int
var layers []layer

const TYPE_DIR = 1
const TYPE_REG = 2
const TYPE_SYMLINK = 3
const TYPE_HARDLINK = 4
const TYPE_CHAR = 5
const TYPE_BLOCK = 6
const TYPE_FIFO = 7

var types = map[string]int{
	"dir":      TYPE_DIR,
	"reg":      TYPE_REG,
	"symlink":  TYPE_SYMLINK,
	"hardlink": TYPE_HARDLINK,
	"char":     TYPE_CHAR,
	"block":    TYPE_BLOCK,
	"fifo":     TYPE_FIFO,
}

func errorValue(err error) int {
	cause := errors.Cause(err)

	if e, ok := cause.(syscall.Errno); ok {
		return -int(e)
	}

	return -int(syscall.EINVAL)
}

func (r *urlReaderAt) hasChunks(off, len int64) bool {
	if r.done {
		return true
	}
	for n := off / chunkSize; n <= (off+len)/chunkSize; n++ {
		if !r.chunks[n] {
			return false
		}
	}
	return true
}
func (r *urlReaderAt) fetchChunk(nChunk int64) error {
	off := nChunk * chunkSize
	len := (nChunk + 1) * chunkSize

	if off+len > r.contentLength {
		len = r.contentLength - off
	}

	rangeVal := fmt.Sprintf("bytes=%d-%d", off, off+len-1)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequest("GET", r.url, nil)
	if err != nil {
		return err
	}
	req = req.WithContext(ctx)
	req.Header.Set("Range", rangeVal)

	if r.client == nil {
		r.client = &http.Client{
			Timeout: 30 * time.Second,
		}
	}

	res, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if location := res.Header.Get("Location"); location != "" {
		r.url = location
		return r.fetchChunk(nChunk)
	}

	buf := make([]byte, len)

	n, err := io.ReadFull(res.Body, buf)
	if err != nil {
		return err
	}

	_, err = r.destFileFD.WriteAt(buf[:n], off)
	return err
}

func (r *urlReaderAt) download() error {
	defer func() {
		r.done = true
		r.cond.L.Lock()
		r.cond.Broadcast()
		r.cond.L.Unlock()
	}()

	genC := make(chan int64)
	done := make(chan interface{})

	if downloadAllFile {
		go func() {
			// iterate backward as the TOC is at the end
			for nChunk := r.contentLength / chunkSize; nChunk >= int64(0); nChunk-- {
				genC <- nChunk
			}
			done <- true
		}()
	}

	for {
		var nextChunk int64
		select {
		case n := <-r.requests:
			nextChunk = n
		case n := <-genC:
			nextChunk = n
		case <-done:
			return nil
		}

		if r.chunks[nextChunk] {
			continue
		}

		if err := r.fetchChunk(nextChunk); err != nil {
			return err
		}
		r.chunks[nextChunk] = true
		r.cond.L.Lock()
		r.cond.Broadcast()
		r.cond.L.Unlock()
	}
	return nil
}

func (r *urlReaderAt) requestChunks(off, len int64) {
	for n := off / chunkSize; n <= (off+len)/chunkSize; n++ {
		if !r.chunks[n] {
			r.requests <- n
		}
	}
}

func (r *urlReaderAt) ReadAt(p []byte, off int64) (n int, err error) {
	r.cond.L.Lock()
	requested := false
	for !r.hasChunks(off, int64(len(p))) {
		if !requested {
			r.requestChunks(off, int64(len(p)))
			requested = true
		}
		r.cond.Wait()
	}
	r.cond.L.Unlock()

	return r.destFileFD.ReadAt(p, off)
}

func doLookup(l *layer, path string) (*stargz.TOCEntry, bool) {
	if path == "." {
		path = ""
	}
	ent, ok := l.reader.Lookup(path)
	if !ok {
		return l.reader.Lookup(path + "/")
	}
	return ent, ok

}

func openLayer(data, workdir string) (*io.SectionReader, error) {
	if strings.HasPrefix(data, "file://") {
		path := data[len("file://"):]
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}

		fi, err := f.Stat()
		if err != nil {
			return nil, err
		}
		return io.NewSectionReader(f, 0, fi.Size()), nil
	}
	if strings.HasPrefix(data, "https://") || strings.HasPrefix(data, "http://") {
		res, err := http.Head(data)
		if err != nil {
			return nil, err
		}
		if res.ContentLength == 0 {
			return nil, fmt.Errorf("invalid Content-Length for %s", data)
		}

		m := sync.Mutex{}
		cond := sync.NewCond(&m)
		destFile := filepath.Join(workdir, filepath.Base(data))
		destFileFD, err := os.OpenFile(destFile, os.O_RDWR|os.O_CREATE, 0700)
		chunks := make([]bool, res.ContentLength/chunkSize+1)
		r := &urlReaderAt{
			url:           data,
			contentLength: res.ContentLength,
			cond:          cond,
			destFile:      destFile,
			destFileFD:    destFileFD,
			chunks:        chunks,
			requests:      make(chan int64, 10),
		}
		go r.download()

		return io.NewSectionReader(r, 0, res.ContentLength), nil
	}
	return nil, fmt.Errorf("source %s is not supported", data)
}

func getParentDir(s string) string {
	p := filepath.Dir(strings.TrimSuffix(s, "/"))
	if p == "." {
		return ""
	}
	return p
}

func getInoFor(l *layer, ent *stargz.TOCEntry) (uint64, error) {
	ino, ok := l.ino[ent.Name]
	if ok {
		return ino, nil
	}
	if ent.Name != "" {
		p := getParentDir(ent.Name)
		pnode, ok := doLookup(l, p)
		if !ok {
			return 0, syscall.ENOENT
		}

		_, err := getInoFor(l, pnode)
		if err != nil {
			return 0, err
		}
	}

	destpath := filepath.Join(l.target, ent.Name)
	switch ent.Type {
	case "dir":
		if err := os.Mkdir(destpath, os.FileMode(ent.Mode)); err != nil && !os.IsExist(err) {
			return 0, err
		}
	case "char", "block", "fifo", "reg":
		f, err := os.OpenFile(destpath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, os.FileMode(ent.Mode))
		if err != nil && !os.IsExist(err) {
			return 0, err
		}
		f.Close()
	case "symlink":
		if err := os.Symlink(ent.LinkName, destpath); err != nil && !os.IsExist(err) {
			return 0, err
		}
	case "link":
		target, ok := l.reader.Lookup(ent.LinkName)
		if !ok {
			return 0, syscall.ENOENT
		}

		ino, err := getInoFor(l, target)
		if err != nil {
			return 0, err
		}

		if err := os.Link(ent.LinkName, destpath); err != nil && !os.IsExist(err) {
			return 0, err
		}
		l.ino[ent.Name] = ino
		return ino, nil
	}

	st, err := os.Lstat(destpath)
	if err != nil {
		return 0, err
	}

	ino = st.Sys().(*syscall.Stat_t).Ino
	l.ino[ent.Name] = ino
	return ino, nil
}

//export OpenLayer
func OpenLayer(data, target, workdir string) int {
	sr, err := openLayer(data, workdir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot open source %s: %v\n", data, err)
		os.Exit(1)
	}

	r, err := stargz.Open(sr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot open stargz file %v: %v\n", data, err)
		os.Exit(1)
	}
	newLayer := layer{
		reader:  r,
		target:  target,
		workdir: workdir,
		ino:     make(map[string]uint64),
	}
	layers = append(layers, newLayer)
	return len(layers) - 1
}

//export OpenLayerBase64
func OpenLayerBase64(dataB64, target, workdir string) int {
	data, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot decode %s\n", dataB64)
		os.Exit(1)
	}
	return OpenLayer(string(data), target, workdir)
}

//export Stat
func Stat(layerHandle int, path string) (ret int, st_type, st_ino, st_mode, st_nlink, st_uid, st_gid, st_rdev_major, st_rdev_minor, st_size uint64, st_time int64) {
	layer := &layers[layerHandle]

	ent, ok := doLookup(layer, path)
	if !ok {
		ret = -int(syscall.ENOENT)
		return
	}

	st_type = uint64(types[ent.Type])

	var err error
	st_ino, err = getInoFor(layer, ent)
	if err != nil {
		ret = errorValue(err)
		return
	}
	st_mode = uint64(ent.Mode)
	st_nlink = uint64(ent.NumLink)
	st_uid = uint64(ent.Uid)
	st_gid = uint64(ent.Gid)
	st_rdev_major = uint64(ent.DevMajor)
	st_rdev_minor = uint64(ent.DevMinor)
	st_size = uint64(ent.Size)
	st_time = ent.ModTime().UnixNano()
	ret = 0
	return
}

func getDentType(t string) int {
	switch t {
	case "dir":
		return syscall.DT_DIR
	case "reg":
		return syscall.DT_REG
	case "symlink":
		return syscall.DT_LNK
	case "link":
		return syscall.DT_REG
	case "char":
		return syscall.DT_CHR
	case "block":
		return syscall.DT_BLK
	case "fifo":
		return syscall.DT_FIFO
	}
	return syscall.DT_UNKNOWN
}

func fillDir(l *layer, d *dir, e *stargz.TOCEntry) error {
	var outerError error
	e.ForeachChild(func(baseName string, ent *stargz.TOCEntry) bool {
		ino, err := getInoFor(l, ent)
		if err != nil {
			outerError = err
			return false
		}
		de := dent{
			ino:  ino,
			name: baseName,
			typ:  getDentType(ent.Type),
		}
		d.childs = append(d.childs, de)
		return true
	})
	return outerError
}

//export OpenDir
func OpenDir(layerHandle int, path string) int {
	layer := &layers[layerHandle]

	ent, ok := doLookup(layer, path)
	if !ok {
		return -int(syscall.ENOENT)
	}

	for {
		dirHandle = dirHandle + 1
		if dirHandle < 0 {
			dirHandle = 0
		}
		if _, ok := dirs[dirHandle]; !ok {
			break
		}
	}

	d := dir{}
	err := fillDir(layer, &d, ent)
	if err != nil {
		return errorValue(err)
	}
	dirs[dirHandle] = &d
	return dirHandle
}

//export CloseDir
func CloseDir(handle int) int {
	delete(dirs, handle)
	return 0
}

//export ReadDir
func ReadDir(handle int) (ret int, ino uint64, name *C.char, typ int) {
	d, ok := dirs[handle]
	if !ok {
		ret = -int(syscall.EINVAL)
		return
	}

	if d.pos == len(d.childs) {
		ret = 0
		return
	}

	c := d.childs[d.pos]

	ino = c.ino
	name = C.CString(strings.TrimSuffix(c.name, "/"))
	typ = c.typ
	ret = 1

	d.pos = d.pos + 1
	return
}

//export WaitForFile
func WaitForFile(layerHandle int, path string) (ret int) {
	if path == "." {
		path = ""
	}
	l := &layers[layerHandle]

	ent, ok := doLookup(l, path)
	if !ok {
		return -int(syscall.ENOENT)
	}

	if ent.Type != "reg" && ent.Type != "link" {
		return 0
	}

	// Make sure parent directories exist
	_, err := getInoFor(l, ent)
	if err != nil {
		return errorValue(err)
	}

	origpath := filepath.Join(l.target, path)
	destpath := filepath.Join(l.target, ent.Name)
	st, err := os.Lstat(destpath)
	if err != nil {
		return -int(syscall.ENOENT)
	}

	if origpath != destpath {
		if err := os.Link(destpath, origpath); err != nil && !os.IsExist(err) {
			return errorValue(err)
		}
	}

	size := st.Sys().(*syscall.Stat_t).Size
	if ent.Size != size {
		reader, err := l.reader.OpenFile(path)
		if err != nil {
			return -int(syscall.ENOENT)
		}
		dest, err := os.OpenFile(destpath, os.O_RDWR|os.O_TRUNC, 0700)
		if err != nil {
			return -int(syscall.ENOENT)
		}
		if _, err := io.Copy(dest, reader); err != nil {
			return -int(syscall.ENOENT)
		}
	}

	return 0
}

//export Getxattr
func Getxattr(layerHandle int, path, name string) (ret int, value *C.char) {
	if path == "." {
		path = ""
	}
	l := &layers[layerHandle]
	ent, ok := doLookup(l, path)
	if !ok {
		return -int(syscall.ENOENT), nil
	}
	v, ok := ent.Xattrs[name]
	if !ok {
		return -int(syscall.ENODATA), nil
	}
	return len(v), C.CString(string(v))
}

//export Listxattr
func Listxattr(layerHandle int, path string) (ret int, value unsafe.Pointer) {
	if path == "." {
		path = ""
	}
	l := &layers[layerHandle]
	ent, ok := doLookup(l, path)
	if !ok {
		return -int(syscall.ENOENT), nil
	}

	var data []byte
	for k, _ := range ent.Xattrs {
		data = append(data, k...)
		data = append(data, 0)
	}
	data = append(data, 0)

	return len(data), C.CBytes(data)
}

func main() {
}
