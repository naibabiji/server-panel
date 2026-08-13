package executor

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestListStorageDevicesClassifiesSystemAndDataDisks(t *testing.T) {
	original := storageCommand
	t.Cleanup(func() { storageCommand = original })
	storageCommand = func(name string, args ...string) ([]byte, error) {
		if name == "findmnt" {
			return []byte("/dev/vda2\n"), nil
		}
		return []byte(`{"blockdevices":[
            {"name":"vda","path":"/dev/vda","pkname":null,"size":10737418240,"type":"disk","fstype":null,"label":null,"uuid":null,"mountpoints":[],"model":"System","serial":"sys","ro":false,"children":[
                {"name":"vda1","path":"/dev/vda1","pkname":"vda","size":536870912,"type":"part","fstype":"vfat","label":null,"uuid":"efi","mountpoints":["/boot/efi"],"model":null,"serial":null,"ro":false},
                {"name":"vda2","path":"/dev/vda2","pkname":"vda","size":10200547328,"type":"part","fstype":"ext4","label":null,"uuid":"root","mountpoints":["/"],"model":null,"serial":null,"ro":false}
            ]},
            {"name":"vdb","path":"/dev/vdb","pkname":null,"size":430570471424,"type":"disk","fstype":null,"label":null,"uuid":null,"mountpoints":[],"model":"Block Storage","serial":"data","ro":false,"children":[
                {"name":"vdb1","path":"/dev/vdb1","pkname":"vdb","size":430569422848,"type":"part","fstype":"ext4","label":null,"uuid":"data","mountpoints":[],"model":null,"serial":null,"ro":false}
            ]}
        ]}`), nil
	}

	devices, err := ListStorageDevices()
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]StorageDevice{}
	for _, device := range devices {
		byPath[device.Path] = device
	}
	if !byPath["/dev/vda"].System || !byPath["/dev/vda2"].System {
		t.Fatalf("system device chain not protected: %#v", byPath)
	}
	if byPath["/dev/vdb"].CanInitialize {
		t.Fatal("partitioned data disk must not be offered for initialization")
	}
	if !byPath["/dev/vdb1"].CanMount {
		t.Fatalf("formatted data partition should be mountable: %#v", byPath["/dev/vdb1"])
	}
}

func TestValidateMountPoint(t *testing.T) {
	for _, path := range []string{"/mnt/backup", "/mnt/data_01", "/mnt/vultr-block-storage"} {
		if err := ValidateMountPoint(path); err != nil {
			t.Fatalf("ValidateMountPoint(%q): %v", path, err)
		}
	}
	for _, path := range []string{"/mnt", "/srv/backup", "/mnt/a/b", "/mnt/../etc", "/mnt/备份", "/mnt/with space"} {
		if err := ValidateMountPoint(path); err == nil {
			t.Fatalf("ValidateMountPoint(%q) = nil, want error", path)
		}
	}
}

func TestProtectedDeviceNamesIncludesRootAncestorsOnly(t *testing.T) {
	pkVDA := "vda"
	devices := []lsblkDevice{
		{Name: "vda", Type: "disk", Children: []lsblkDevice{{Name: "vda1", PKName: &pkVDA, Type: "part"}, {Name: "vda2", PKName: &pkVDA, Type: "part"}}},
		{Name: "vdb", Type: "disk"},
	}
	got := protectedDeviceNames(devices, "/dev/vda2")
	if !got["vda2"] || !got["vda"] {
		t.Fatalf("root chain not protected: %#v", got)
	}
	if !got["vda1"] {
		t.Fatalf("system disk sibling partition not protected: %#v", got)
	}
	if got["vdb"] {
		t.Fatalf("unrelated data disk protected: %#v", got)
	}
}

func TestProtectedDeviceNamesHandlesBtrfsSubvolume(t *testing.T) {
	pkSDA := "sda"
	devices := []lsblkDevice{
		{Name: "sda", Type: "disk", Children: []lsblkDevice{{Name: "sda1", PKName: &pkSDA, Type: "part"}, {Name: "sda2", PKName: &pkSDA, Type: "part"}}},
		{Name: "sdb", Type: "disk"},
	}
	got := protectedDeviceNames(devices, "/dev/sda2[/@]")
	if !got["sda"] || !got["sda1"] || !got["sda2"] {
		t.Fatalf("btrfs root disk not fully protected: %#v", got)
	}
	if got["sdb"] {
		t.Fatalf("unrelated data disk protected: %#v", got)
	}
}

func TestProtectedDeviceNamesFailsClosedForUnknownRoot(t *testing.T) {
	devices := []lsblkDevice{{Name: "vda", Type: "disk"}, {Name: "vdb", Type: "disk"}}
	got := protectedDeviceNames(devices, "/dev/mapper/unknown-root")
	if !got["vda"] || !got["vdb"] {
		t.Fatalf("unknown root source must protect every device: %#v", got)
	}
}

func TestDestructiveActionsRejectInvalidMountPointBeforeCommands(t *testing.T) {
	original := storageCommand
	t.Cleanup(func() { storageCommand = original })
	called := false
	storageCommand = func(name string, args ...string) ([]byte, error) {
		called = true
		return nil, errors.New("must not run")
	}
	if err := FormatAndMountPartition("/dev/vdb1", "/dev/vdb1", ""); err == nil {
		t.Fatal("invalid mount point accepted")
	}
	if called {
		t.Fatal("a command ran before mount point validation")
	}
	if err := InitializeAndMountDisk("/dev/vdb", "/dev/vdb", "/srv/backup"); err == nil {
		t.Fatal("invalid mount point accepted")
	}
	if called {
		t.Fatal("a command ran before mount point validation")
	}
}

func TestPrepareMountDestinationRejectsActiveMount(t *testing.T) {
	original := storageCommand
	t.Cleanup(func() { storageCommand = original })
	storageCommand = func(name string, args ...string) ([]byte, error) {
		if name == "findmnt" {
			return []byte("/mnt/already-mounted\n"), nil
		}
		return nil, errors.New("unexpected command")
	}
	if err := prepareMountDestination("/mnt/already-mounted"); err == nil {
		t.Fatal("active mount target accepted")
	}
}

func TestManagedFstabRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fstab")
	if err := os.WriteFile(path, []byte("UUID=root / ext4 defaults 0 1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	line := "UUID=data-uuid /mnt/backup ext4 defaults,nofail,noatime 0 2"
	if err := addManagedFstabLine(path, line); err != nil {
		t.Fatal(err)
	}
	managed, err := managedFstabUUIDs(path)
	if err != nil || !managed["data-uuid"] {
		t.Fatalf("managed=%v err=%v", managed, err)
	}
	if err := removeManagedFstabLine(path, "data-uuid", "/mnt/backup"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "UUID=root / ext4 defaults 0 1\n" {
		t.Fatalf("unexpected fstab after removal: %q", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0644 {
		t.Fatalf("fstab mode changed: %v", info.Mode().Perm())
	}
}
