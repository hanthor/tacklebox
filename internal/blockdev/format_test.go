package blockdev

import "testing"

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
