#define _GNU_SOURCE

#include <stdint.h>

#include <stdlib.h>
#include <stdio.h>
#include <fcntl.h>
#include <unistd.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/types.h>

#include <errno.h>

#include <sys/xattr.h>

#include "fuse-overlayfs.h"
#include "utils.h"
#include <sys/sysmacros.h>

#include <go.h>

#define TYPE_DIR      1
#define TYPE_REG      2
#define TYPE_SYMLINK  3
#define TYPE_HARDLINK 4
#define TYPE_CHAR     5
#define TYPE_BLOCK    6
#define TYPE_FIFO     7

static int
get_handle (struct ovl_layer *l)
{
  return (int) (long) (l->data_source_private_data);
}

static GoString
make_go_string (const char *v)
{
  GoString s;

  s.p = v;
  s.n = strlen (v);

  return s;
}

static int
crfs_file_exists (struct ovl_layer *l, const char *pathname)
{
  struct Stat_return ret;
  int layer_handle;

  layer_handle = get_handle (l);

  ret = Stat (layer_handle, make_go_string (pathname));

  if (ret.r0 < 0)
    errno = -ret.r0;

  return ret.r0;
}

static int
crfs_listxattr (struct ovl_layer *l, const char *path, char *buf, size_t size)
{
  int layer_handle;
  struct Listxattr_return r;

  layer_handle = get_handle (l);

  r = Listxattr (layer_handle, make_go_string (path));
  if (r.r0 < 0)
    {
      errno = -r.r0;
      return -1;
    }

  if (size == 0)
    {
      free (r.r1);
      return r.r0;
    }

  if (size < r.r0)
    {
      free (r.r1);
      errno = ERANGE;
      return -1;
    }

  memcpy (buf, r.r1, r.r0);
  free (r.r1);
  return r.r0;
}

static int
crfs_getxattr (struct ovl_layer *l, const char *path, const char *name, char *buf, size_t size)
{
  int layer_handle;
  struct Getxattr_return r;

  layer_handle = get_handle (l);

  r = Getxattr (layer_handle, make_go_string (path), make_go_string (name));
  if (r.r0 < 0)
    {
      errno = -r.r0;
      return -1;
    }

  if (size == 0)
    {
      free (r.r1);
      return r.r0;
    }

  if (size < r.r0)
    {
      free (r.r1);
      errno = ERANGE;
      return -1;
    }
  
  memcpy (buf, r.r1, r.r0);
  free (r.r1);
  return r.r0;
}

static int
crfs_statat (struct ovl_layer *l, const char *path, struct stat *st, int flags, unsigned int mask)
{
  struct Stat_return ret;
  int layer_handle;
  mode_t type = 0;

  layer_handle = get_handle (l);

  ret = Stat (layer_handle, make_go_string (path));
  if (ret.r0 < 0)
    {
      errno = -ret.r0;
      return -1;
    }

  switch (ret.r1)
    {
    case TYPE_DIR:
      type = S_IFDIR;
      break;

    case TYPE_REG:
    case TYPE_HARDLINK:
      type = S_IFREG;
      break;

    case TYPE_SYMLINK:
      type = S_IFLNK;
      break;

    case TYPE_CHAR:
      type = S_IFCHR;
      break;

    case TYPE_BLOCK:
      type = S_IFBLK;
      break;

    case TYPE_FIFO:
      type = S_IFIFO;
      break;
    }

  st->st_ino = ret.r2;
  st->st_mode = ret.r3 | type;
  st->st_nlink = ret.r4;
  st->st_uid = ret.r5;
  st->st_gid = ret.r6;
  st->st_rdev = makedev (ret.r7, ret.r8);
  st->st_size = ret.r9;
  st->st_mtim.tv_sec = ret.r10 / 1000000000;
  st->st_mtim.tv_nsec = ret.r10 % 1000000000;

  st->st_ctim = st->st_atim = st->st_mtim;

  return 0;
}

static int
crfs_fstat (struct ovl_layer *l, int fd, const char *path, unsigned int mask, struct stat *st)
{
  return crfs_statat (l, path, st, 0, mask);
}

struct dir_data
{
  int handle;
  struct dirent d;
};

static struct dirent *
crfs_readdir (void *dirp)
{
  struct dir_data *dd = (struct dir_data *) dirp;
  int dir_handle;
  struct ReadDir_return r;
  struct dirent *de = &dd->d;

  r = ReadDir (dd->handle);
  if (r.r0 <= 0)
    {
      errno = -r.r0;
      return NULL;
    }

  de->d_ino = r.r1;

  strncpy (de->d_name, r.r2, 255);
  de->d_name[255] = '\0';
  free (r.r2);

  de->d_reclen = sizeof (struct dirent);

  de->d_type = r.r3;

  return de;
}

static void *
crfs_opendir (struct ovl_layer *l, const char *path)
{
  int d;
  int layer_handle;
  struct dir_data *dd;

  layer_handle = get_handle (l);

  if (strcmp (path, ".") == 0)
    path = "";

  d = OpenDir (layer_handle, make_go_string (path));
  if (d < 0)
    return NULL;

  dd = malloc (sizeof (struct dir_data));
  if (dd == NULL)
    {
      CloseDir (d);
      return NULL;
    }

  dd->handle = d;

  return dd;
}

static int
crfs_closedir (void *dirp)
{
  int dir_handle;
  struct dir_data *dd = (struct dir_data *) dirp;

  CloseDir (dd->handle);
  free (dd);

  return 0;
}

static int
crfs_openat (struct ovl_layer *l, const char *path, int flags, mode_t mode)
{
  int layer_handle;
  int ret;

  layer_handle = get_handle (l);

  ret = WaitForFile (layer_handle, make_go_string (path));
  if (ret < 0)
    return ret;
  return TEMP_FAILURE_RETRY (openat (l->fd, path, flags, mode));
}

static ssize_t
crfs_readlinkat (struct ovl_layer *l, const char *path, char *buf, size_t bufsiz)
{
  return TEMP_FAILURE_RETRY (readlinkat (l->fd, path, buf, bufsiz));
}

static int
crfs_load_data_source (struct ovl_layer *l, const char *opaque, const char *path, int layer)
{
  int layer_handle;

  l->path = realpath (path, NULL);
  if (l->path == NULL)
    return -1;

  l->fd = open (l->path, O_DIRECTORY);
  if (l->fd < 0)
    {
      free (l->path);
      l->path = NULL;
      return l->fd;
    }

  layer_handle = OpenLayer (make_go_string (opaque),
                            make_go_string (l->path),
                            make_go_string (l->ovl_data->workdir),
                            layer);
  if (layer_handle < 0)
    {
      errno = -layer_handle;
      return -1;
    }

  l->data_source_private_data = (void *) (long) layer_handle;

  return 0;
}

static int
crfs_num_of_layers (const char *opaque, const char *path)
{
  return NumOfLayers (make_go_string (opaque), make_go_string (path));
}

static int
crfs_cleanup (struct ovl_layer *l)
{
  return 0;
}

struct data_source crfs_ds =
  {
   .num_of_layers = crfs_num_of_layers,
   .load_data_source = crfs_load_data_source,
   .cleanup = crfs_cleanup,
   .file_exists = crfs_file_exists,
   .statat = crfs_statat,
   .fstat = crfs_fstat,
   .opendir = crfs_opendir,
   .readdir = crfs_readdir,
   .closedir = crfs_closedir,
   .openat = crfs_openat,
   .getxattr = crfs_getxattr,
   .listxattr = crfs_listxattr,
   .readlinkat = crfs_readlinkat,
};

int
plugin_version ()
{
  return 1;
}

const char *
plugin_name ()
{
  return "crfs";
}

struct data_source *plugin_load (struct ovl_layer *layer, const char *opaque, const char *path)
{
  Load ();
  return &crfs_ds;
}

int
plugin_release ()
{
  Release ();
  return 0;
}
