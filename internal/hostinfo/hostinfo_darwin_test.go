package hostinfo

import (
	"reflect"
	"testing"
)

const diskutilList = `/dev/disk0 (internal, physical):
   #:                       TYPE NAME                    SIZE       IDENTIFIER
   0:      GUID_partition_scheme                        *500.3 GB   disk0
/dev/disk1 (external, physical):
   0:      GUID_partition_scheme                        *2.0 TB     disk1
/dev/disk3 (synthesized):
   0:      APFS Container Scheme -                      +494.4 GB   disk3
/dev/disk4 (disk image):
`

const diskutilInfo = `   Device Identifier:         disk0
   Device / Media Name:       APPLE SSD AP0512Z

   Disk Size:                 500.3 GB (500277792768 Bytes) (exactly 977105064 512-Byte-Units)
`

func TestParseDiskList(t *testing.T) {
	got := parseDiskList(diskutilList)
	want := []string{"disk0", "disk1", "disk4"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseDiskList = %v, want %v", got, want)
	}
}

func TestParseDiskInfo(t *testing.T) {
	size, vendor := parseDiskInfo(diskutilInfo)
	if size != 465 || vendor != "APPLE SSD AP0512Z" {
		t.Errorf("parseDiskInfo = %d %q, want 465 %q", size, vendor, "APPLE SSD AP0512Z")
	}
	if size, vendor := parseDiskInfo(""); size != 0 || vendor != "" {
		t.Errorf("parseDiskInfo(empty) = %d %q", size, vendor)
	}
}
