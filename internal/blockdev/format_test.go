package blockdev

import (
	"os"
	"strings"
	"testing"
)

func TestPartitionPath(t *testing.T) {
	cases := []struct {
		device string
		n      int
		want   string
	}{
		{"/dev/sda", 1, "/dev/sda1"},
		{"/dev/sdb", 3, "/dev/sdb3"},
		{"/dev/nvme0n1", 1, "/dev/nvme0n1p1"},
		{"/dev/nvme1n1", 2, "/dev/nvme1n1p2"},
		{"/dev/loop0", 1, "/dev/loop0p1"},
		{"/dev/loop12", 3, "/dev/loop12p3"},
		{"/dev/mmcblk0", 2, "/dev/mmcblk0p2"},
		{"/dev/md0", 1, "/dev/md0p1"},
	}
	for _, tc := range cases {
		got := PartitionPath(tc.device, tc.n)
		if got != tc.want {
			t.Errorf("PartitionPath(%q, %d) = %q, want %q", tc.device, tc.n, got, tc.want)
		}
	}
}

// TestReadMounts checks the /proc/mounts parser on a synthetic input.
func TestReadMounts(t *testing.T) {
	content := "sysfs /sys sysfs rw 0 0\n/dev/sdb1 /media/james/USB vfat rw 0 0\n/dev/sdb2 /media/james/DATA ext4 rw 0 0\n/dev/sda1 / ext4 rw 0 0\n"
	f, err := os.CreateTemp("", "mounts*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString(content)
	f.Close()

	original := mountsFile
	mountsFile = f.Name()
	defer func() { mountsFile = original }()

	mounts, err := readMounts()
	if err != nil {
		t.Fatal(err)
	}
	if len(mounts) != 4 {
		t.Fatalf("expected 4 mounts, got %d", len(mounts))
	}
	if mounts[1].source != "/dev/sdb1" || mounts[1].target != "/media/james/USB" {
		t.Errorf("unexpected mount[1]: %+v", mounts[1])
	}
}

// TestUnmountDeviceSkipsNonDev verifies that non-/dev/ paths are skipped.
func TestUnmountDeviceSkipsNonDev(t *testing.T) {
	if err := UnmountDevice("/tmp/tacklebox.img"); err != nil {
		t.Errorf("unexpected error for loop image path: %v", err)
	}
}

// TestUnmountDeviceFiltersCorrectly verifies the partition matching logic.
func TestUnmountDeviceFiltersCorrectly(t *testing.T) {
	cases := []struct {
		device string
		source string
		want   bool
	}{
		{"/dev/sdb", "/dev/sdb1", true},
		{"/dev/sdb", "/dev/sdb2", true},
		{"/dev/sdb", "/dev/sdb", true},
		{"/dev/sdb", "/dev/sdc1", false},
		{"/dev/nvme0n1", "/dev/nvme0n1p1", true},
		{"/dev/nvme0n1", "/dev/nvme0n2p1", false},
	}
	for _, tc := range cases {
		got := tc.source == tc.device || strings.HasPrefix(tc.source, tc.device)
		if got != tc.want {
			t.Errorf("device=%q source=%q: match=%v, want %v", tc.device, tc.source, got, tc.want)
		}
	}
}
