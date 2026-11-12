//go:build linux

package storage

import (
	"reflect"
	"strings"
	"testing"

	"github.com/moby/sys/mountinfo"
)

// The layout of the server this fork runs on (Debian 13, bare metal): / on
// LVM, /boot, /boot/efi, /storage (ext4, holds Docker's data root) and
// /archive (xfs), plus what the kernel, systemd and Docker mount.
const mountinfoServer = `24 29 0:22 / /sys rw,nosuid,nodev,noexec,relatime shared:6 - sysfs sysfs rw
25 29 0:23 / /proc rw,nosuid,nodev,noexec,relatime shared:12 - proc proc rw
26 29 0:6 / /dev rw,nosuid,relatime shared:2 - devtmpfs udev rw,size=65831040k,nr_inodes=16457760,mode=755,inode64
27 26 0:24 / /dev/pts rw,nosuid,noexec,relatime shared:3 - devpts devpts rw,gid=5,mode=600,ptmxmode=000
28 29 0:25 / /run rw,nosuid,nodev,noexec,relatime shared:5 - tmpfs tmpfs rw,size=13193312k,mode=755,inode64
29 1 254:0 / / rw,relatime shared:1 - ext4 /dev/mapper/dpe--vg-root rw,errors=remount-ro
30 24 0:7 / /sys/kernel/security rw,nosuid,nodev,noexec,relatime shared:7 - securityfs securityfs rw
31 26 0:26 / /dev/shm rw,nosuid,nodev shared:4 - tmpfs tmpfs rw,inode64
32 24 0:27 / /sys/fs/cgroup rw,nosuid,nodev,noexec,relatime shared:8 - cgroup2 cgroup2 rw,nsdelegate,memory_recursiveprot
33 24 0:28 / /sys/fs/pstore rw,nosuid,nodev,noexec,relatime shared:9 - pstore pstore rw
34 24 0:29 / /sys/firmware/efi/efivars rw,nosuid,nodev,noexec,relatime shared:10 - efivarfs efivarfs rw
35 24 0:30 / /sys/fs/bpf rw,nosuid,nodev,noexec,relatime shared:11 - bpf bpf rw,mode=700
36 25 0:31 / /proc/sys/fs/binfmt_misc rw,relatime shared:13 - autofs systemd-1 rw,fd=41,pgrp=1,timeout=0,minproto=5,maxproto=5,direct,pipe_ino=1017
37 26 0:32 / /dev/hugepages rw,nosuid,nodev,relatime shared:14 - hugetlbfs hugetlbfs rw,pagesize=2M
38 26 0:21 / /dev/mqueue rw,nosuid,nodev,noexec,relatime shared:15 - mqueue mqueue rw
39 24 0:8 / /sys/kernel/debug rw,nosuid,nodev,noexec,relatime shared:16 - debugfs debugfs rw
40 28 0:33 / /run/lock rw,nosuid,nodev,noexec,relatime shared:17 - tmpfs tmpfs rw,size=5120k,inode64
41 24 0:13 / /sys/kernel/tracing rw,nosuid,nodev,noexec,relatime shared:18 - tracefs tracefs rw
64 28 0:35 / /run/credentials/systemd-journald.service ro,nosuid,nodev,noexec,relatime,nosymfollow shared:20 - tmpfs tmpfs rw,size=1024k,nr_inodes=1024,mode=700,inode64,noswap
42 24 0:34 / /sys/fs/fuse/connections rw,nosuid,nodev,noexec,relatime shared:19 - fusectl fusectl rw
65 24 0:36 / /sys/kernel/config rw,nosuid,nodev,noexec,relatime shared:21 - configfs configfs rw
45 29 0:41 / /tmp rw,nosuid,nodev shared:49 - tmpfs tmpfs rw,size=65966552k,nr_inodes=1048576,inode64
48 29 8:2 / /boot rw,relatime shared:88 - ext4 /dev/sda2 rw
51 48 8:1 / /boot/efi rw,relatime shared:91 - vfat /dev/sda1 rw,fmask=0077,dmask=0077,codepage=437,iocharset=ascii,shortname=mixed,utf8,errors=remount-ro
55 36 0:43 / /proc/sys/fs/binfmt_misc rw,nosuid,nodev,noexec,relatime shared:97 - binfmt_misc binfmt_misc rw
47 28 0:45 / /run/user/0 rw,nosuid,nodev,relatime shared:50 - tmpfs tmpfs rw,size=13193308k,nr_inodes=3298327,mode=700,inode64
62 29 8:3 / /storage rw,relatime shared:150 - ext4 /dev/sda3 rw
68 29 8:17 / /archive rw,nosuid,nodev,relatime shared:154 - xfs /dev/sdb1 rw,attr2,inode64,logbufs=8,logbsize=32k,noquota
305 39 0:13 / /sys/kernel/debug/tracing rw,nosuid,nodev,noexec,relatime shared:198 - tracefs tracefs rw
682 28 0:4 net:[4026531840] /run/docker/netns/default rw shared:206 - nsfs nsfs rw
297 62 0:49 / /storage/docker/rootfs/overlayfs/9be6024c8f4acfce9c4c3c963e1fccdca3d866bd00d3e4c122fd9996faf9f79a rw,relatime shared:166 - overlay overlay rw,lowerdir=/storage/containerd/io.containerd.snapshotter.v1.overlayfs/snapshots/155/fs:/storage/containerd/io.containerd.snapshotter.v1.overlayfs/snapshots/15/fs,upperdir=/storage/containerd/io.containerd.snapshotter.v1.overlayfs/snapshots/156/fs,workdir=/storage/containerd/io.containerd.snapshotter.v1.overlayfs/snapshots/156/work
301 28 0:4 net:[4026533081] /run/docker/netns/af4dca683626 rw shared:170 - nsfs nsfs rw
`

// Disks the way upstream expects them (under /mnt), a NAS share, a second
// mount and a bind mount of a disk that is already listed, a Docker volume on
// its own device, removable media, a snap and a desktop FUSE helper.
const mountinfoMixed = `22 28 0:21 / /sys rw,nosuid,nodev,noexec,relatime shared:7 - sysfs sysfs rw
23 28 0:22 / /proc rw,nosuid,nodev,noexec,relatime shared:13 - proc proc rw
28 1 259:2 / / rw,relatime shared:1 - ext4 /dev/nvme0n1p2 rw,errors=remount-ro
90 28 259:1 / /boot/efi rw,relatime shared:30 - vfat /dev/nvme0n1p1 rw,fmask=0077,dmask=0077
91 28 8:1 / /mnt/x rw,relatime shared:31 - ext4 /dev/sda1 rw
92 28 8:17 / /mnt/disk2 rw,relatime shared:32 - xfs /dev/sdb1 rw,attr2,inode64
93 28 0:52 / /srv/nas rw,relatime shared:33 - nfs4 192.168.1.10:/export/media rw,vers=4.2,rsize=1048576,wsize=1048576,hard,proto=tcp,addr=192.168.1.10
94 28 0:53 / /media/photos rw,relatime shared:34 - cifs //nas/photos rw,vers=3.1.1,cache=strict,username=media
95 28 0:54 / /srv/pool rw,nosuid,nodev,relatime shared:35 - fuse.mergerfs a:b rw,user_id=0,group_id=0,default_permissions,allow_other
96 28 8:17 /films /srv/films rw,relatime shared:32 - xfs /dev/sdb1 rw,attr2,inode64
97 28 8:33 / /data rw,relatime shared:36 - ext4 /dev/sdc1 rw
98 28 8:33 / /srv/data-again rw,relatime shared:36 - ext4 /dev/sdc1 rw
99 28 8:49 / /var/lib/docker/volumes/media/_data rw,relatime shared:37 - ext4 /dev/sdd1 rw
100 28 0:60 / /var/lib/docker/overlay2/3f1c/merged rw,relatime shared:38 - overlay overlay rw,lowerdir=/var/lib/docker/overlay2/l/A,upperdir=/var/lib/docker/overlay2/3f1c/diff,workdir=/var/lib/docker/overlay2/3f1c/work
101 28 7:0 / /snap/core22/1621 ro,nodev,relatime shared:39 - squashfs /dev/loop0 ro,errors=continue
102 28 8:65 / /run/media/admin/USB rw,nosuid,nodev,relatime shared:40 - vfat /dev/sde1 rw,uid=1000,gid=1000
103 28 0:61 / /run/user/1000/doc rw,nosuid,nodev,relatime shared:41 - fuse.portal portal rw,user_id=1000,group_id=1000
104 28 11:0 / /media/cdrom ro,nosuid,nodev,relatime shared:42 - iso9660 /dev/sr0 ro,norock
105 28 0:62 / /tank/media rw,noatime shared:43 - zfs tank/media rw,xattr,posixacl
106 28 8:81 / /srv/windows rw,nosuid,nodev,relatime shared:44 - fuseblk /dev/sdf1 rw,user_id=0,group_id=0,allow_other,blksize=4096
`

// Cosmos in a container: the host is mounted at /mnt/host, everything else is
// the container's own.
const mountinfoContainer = `700 600 0:70 / / rw,relatime master:300 - overlay overlay rw,lowerdir=/var/lib/docker/overlay2/l/B,upperdir=/var/lib/docker/overlay2/9a/diff,workdir=/var/lib/docker/overlay2/9a/work
701 700 0:71 / /proc rw,nosuid,nodev,noexec,relatime - proc proc rw
702 700 0:72 / /dev rw,nosuid - tmpfs tmpfs rw,size=65536k,mode=755,inode64
703 700 8:2 /var/lib/docker/volumes/cosmos-config/_data /config rw,relatime master:1 - ext4 /dev/sda2 rw
704 700 8:2 /var/lib/docker/containers/9a/resolv.conf /etc/resolv.conf rw,relatime - ext4 /dev/sda2 rw
705 700 8:2 /var/lib/docker/containers/9a/hostname /etc/hostname rw,relatime - ext4 /dev/sda2 rw
706 700 8:2 / /mnt/host rw,relatime master:1 - ext4 /dev/sda2 rw
707 706 0:22 / /mnt/host/proc rw,nosuid,nodev,noexec,relatime master:13 - proc proc rw
708 706 8:1 / /mnt/host/boot/efi rw,relatime master:30 - vfat /dev/sda1 rw
709 706 8:17 / /mnt/host/mnt/disk1 rw,relatime master:31 - ext4 /dev/sdb1 rw
710 706 8:33 / /mnt/host/srv/media rw,relatime master:32 - xfs /dev/sdc1 rw
711 706 0:80 / /mnt/host/var/lib/docker/overlay2/9a/merged rw,relatime master:300 - overlay overlay rw,lowerdir=/var/lib/docker/overlay2/l/B
`

func parseMountinfo(t *testing.T, sample string) []*mountinfo.Info {
	t.Helper()
	infos, err := mountinfo.GetMountsFromReader(strings.NewReader(sample), nil)
	if err != nil {
		t.Fatalf("sample does not parse: %v", err)
	}
	return infos
}

func TestDataMounts(t *testing.T) {
	cases := []struct {
		name            string
		sample          string
		insideContainer bool
		dockerRoot      string
		want            []string
	}{
		{
			name:       "this server: / on LVM, /storage ext4 with Docker inside, /archive xfs",
			sample:     mountinfoServer,
			dockerRoot: "/storage/docker",
			want:       []string{"/", "/archive", "/storage"},
		},
		{
			name:   "this server, Docker root unknown: its overlay mounts are still left out",
			sample: mountinfoServer,
			want:   []string{"/", "/archive", "/storage"},
		},
		{
			name:   "/mnt disks, NFS, CIFS, mergerfs, ZFS, NTFS; no duplicates, Docker, snap, removable or read-only media",
			sample: mountinfoMixed,
			want: []string{"/", "/data", "/media/photos", "/mnt/disk2", "/mnt/x",
				"/srv/nas", "/srv/pool", "/srv/windows", "/tank/media"},
		},
		{
			name:            "in a container only the host's mounts count, by their host path",
			sample:          mountinfoContainer,
			insideContainer: true,
			want:            []string{"/", "/mnt/disk1", "/srv/media"},
		},
		{
			name:   "the same table read as bare metal: bind mounts of files are not disks",
			sample: mountinfoContainer,
			want:   []string{"/mnt/host", "/mnt/host/boot/efi", "/mnt/host/mnt/disk1", "/mnt/host/srv/media"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mounts := DataMounts(hostMounts(parseMountinfo(t, c.sample), c.insideContainer), c.dockerRoot)
			got := []string{}
			for _, mount := range mounts {
				got = append(got, mount.Path)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("data mounts = %v, want %v", got, c.want)
			}
		})
	}
}

func TestDataMountsDockerRoot(t *testing.T) {
	mounts := []HostMount{
		{Path: "/", Root: "/", FSType: "ext4", Source: "/dev/sda2", Major: 8, Minor: 2},
		// a disk mounted at the data root itself is a data disk
		{Path: "/data/docker", Root: "/", FSType: "xfs", Source: "/dev/sdb1", Major: 8, Minor: 17},
		// what Docker mounts inside it is not: a volume on its own device, a btrfs layer
		{Path: "/data/docker/volumes/films/_data", Root: "/", FSType: "ext4", Source: "/dev/sdc1", Major: 8, Minor: 33},
		{Path: "/data/docker/btrfs/subvolumes/abc", Root: "/abc", FSType: "btrfs", Source: "/dev/sdd1", Major: 0, Minor: 90},
		// a similar name is another directory
		{Path: "/data/docker-backup", Root: "/", FSType: "ext4", Source: "/dev/sde1", Major: 8, Minor: 65},
	}

	got := []string{}
	for _, mount := range DataMounts(mounts, "/data/docker/") {
		got = append(got, mount.Path)
	}
	want := []string{"/", "/data/docker", "/data/docker-backup"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("data mounts = %v, want %v", got, want)
	}
}

func TestIsDataFilesystem(t *testing.T) {
	cases := []struct {
		fsType string
		source string
		want   bool
	}{
		{"ext4", "/dev/sda3", true},
		{"xfs", "/dev/sdb1", true},
		{"btrfs", "/dev/mapper/vg-data", true},
		{"vfat", "/dev/sdc1", true},
		{"fuseblk", "/dev/sdd1", true},
		{"nfs", "nas:/export", true},
		{"nfs4", "192.168.1.10:/export", true},
		{"cifs", "//nas/share", true},
		{"zfs", "tank/media", true},
		{"fuse.mergerfs", "a:b", true},
		{"fuse.sshfs", "user@host:/data", true},
		{"proc", "proc", false},
		{"sysfs", "sysfs", false},
		{"tmpfs", "tmpfs", false},
		{"devtmpfs", "udev", false},
		{"overlay", "overlay", false},
		{"squashfs", "/dev/loop3", false},
		{"cgroup2", "cgroup2", false},
		{"efivarfs", "efivarfs", false},
		{"bpf", "bpf", false},
		{"tracefs", "tracefs", false},
		{"nsfs", "nsfs", false},
		{"fusectl", "fusectl", false},
		{"autofs", "systemd-1", false},
		{"ramfs", "ramfs", false},
		{"fuse.portal", "portal", false},
		{"fuse.lxcfs", "lxcfs", false},
		{"iso9660", "/dev/sr0", false},
		{"somethingnew", "none", false},
	}

	for _, c := range cases {
		if got := isDataFilesystem(c.fsType, c.source); got != c.want {
			t.Errorf("isDataFilesystem(%q, %q) = %v, want %v", c.fsType, c.source, got, c.want)
		}
	}
}

func TestDockerDataRoot(t *testing.T) {
	cases := []struct {
		config string
		want   string
	}{
		{`{"data-root": "/storage/docker"}`, "/storage/docker"},
		{`{"graph": "/data/docker"}`, "/data/docker"},
		{`{"log-driver": "json-file"}`, ""},
		{`{"data-root": "relative/path"}`, ""},
		{``, ""},
		{`not json`, ""},
	}

	for _, c := range cases {
		if got := dockerDataRoot([]byte(c.config)); got != c.want {
			t.Errorf("dockerDataRoot(%q) = %q, want %q", c.config, got, c.want)
		}
	}
}
