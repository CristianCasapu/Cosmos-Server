package storage

import (
	"fmt"
	// "io/ioutil"
	"strings"
	"strconv"
	"os/exec"
	"io"
	"errors"
	"bytes"

	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/dell/csi-baremetal/pkg/base/linuxutils/lsblk"
	"github.com/sirupsen/logrus"
	"github.com/anatol/smart.go"
)

type SMARTData struct {
	smart.GenericAttributes

	AdditionalData interface{}
	Thresholds interface{}
}

type BlockDevice struct {
	lsblk.BlockDevice
	Children []BlockDevice `json:"children"`
	Usage uint64 `json:"usage"`
	SMART SMARTData `json:"smart"` // Add SMART data field
}

func ListDisks() ([]BlockDevice, error) {
	// Create a new logrus Logger
	logger := logrus.New()

	// Initialize lsblk with the logger
	lsblkExecutor := lsblk.NewLSBLK(logger)

	devices, err := lsblkExecutor.GetBlockDevices("")
	if err != nil {
			return nil, err
	}

	filteredDevices := make([]lsblk.BlockDevice, 0)
	for _, device := range devices {
		if strings.HasPrefix(device.MountPoint, "/snap") || device.MountPoint == "[SWAP]" {
			continue
		}
		filteredDevices = append(filteredDevices, device)
	}

	return GetRecursiveDiskUsageAndSMARTInfo(filteredDevices)
}

// Function to get recursive disk usage and SMART information
func GetRecursiveDiskUsageAndSMARTInfo(devices []lsblk.BlockDevice) ([]BlockDevice, error) {
	devicesF := make([]BlockDevice, len(devices))

	for i, device := range devices {
			used, err := GetDiskUsage(device.Name)
			if err != nil {
					utils.Error("GetRecursiveDiskUsageAndSMARTInfo - Error fetching Disk usage for " + device.Name + " : ", err)
					return nil, err
			}

			devicesF[i].BlockDevice = device
			devicesF[i].Usage = used * uint64(devicesF[i].Size.Int64) / 100

			// SMART information retrieval for NVMe and SATA
			if device.Type == "disk" {
        	dev, err := smart.Open(device.Name)
					if err != nil {
						devicesF[i].Children, _ = GetRecursiveDiskUsageAndSMARTInfo(device.Children)
						continue
					}
					defer dev.Close()

					GenericAttributes, err := dev.ReadGenericAttributes()
					if err != nil {
						utils.Warn("GetRecursiveDiskUsageAndSMARTInfo - Error fetching SMART info for " + device.Name + " : " + err.Error())
						devicesF[i].Children, _ = GetRecursiveDiskUsageAndSMARTInfo(device.Children)
						continue
					}

					smartData := SMARTData{
						GenericAttributes: *GenericAttributes,
						AdditionalData: map[string]string{},
					}

					switch sm := dev.(type) {
						case *smart.SataDevice:
							data, err := sm.ReadSMARTData()
							t, err := sm.ReadSMARTThresholds()
							if err != nil {
								utils.Warn("GetRecursiveDiskUsageAndSMARTInfo - Error fetching SMART info for " + device.Name + " : " + err.Error())
							} else {
								smartData.AdditionalData = data
								devicesF[i].SMART = smartData
								devicesF[i].SMART.Thresholds = *t
							}
						case *smart.NVMeDevice:
							data, err := sm.ReadSMART()
							t, _, err := sm.Identify()
							if err != nil {
								utils.Warn("GetRecursiveDiskUsageAndSMARTInfo - Error fetching SMART info for " + device.Name + " : " + err.Error())
							} else {
								smartData.AdditionalData = data
								smartData.Thresholds = *t
								devicesF[i].SMART = smartData
							}
					}
			}

			// Get usage and SMART info for children
			devicesF[i].Children, _ = GetRecursiveDiskUsageAndSMARTInfo(device.Children)
	}

	return devicesF, nil
}

func GetDiskUsage(path string) (perc uint64, err error) {
	// Get the disk usage using the df command
	cmd := exec.Command("df", "-k", path)

	// Run the command
	output, err := cmd.CombinedOutput()
	if err != nil {
		return 0, err
	}

	// Split the output into lines
	lines := strings.Split(string(output), "\n")
	if len(lines) < 2 {
		return 0, fmt.Errorf("unexpected output: %s", string(output))
	}

	// The output is in the format "Filesystem 1K-blocks Used Available Use% Mounted on"
	// We are interested in the second line
	parts := strings.Fields(lines[1])
	if len(parts) < 5 {
		return 0, fmt.Errorf("unexpected output: %s", string(output))
	}

	// Parse the size (1K-blocks)
	available, err := strconv.ParseUint(parts[3], 10, 64)
	if err != nil {
		return 0, err
	}

	// Parse the used space
	used, err := strconv.ParseUint(parts[2], 10, 64)
	if err != nil {
		return 0, err
	}

	return (used * 100) / (used+available), nil
}

func CreateGPTTable(diskPath string) (io.Reader, error) {
	utils.Log("[STORAGE] Creating GPT table on " + diskPath)
	
	var outputBuffer bytes.Buffer
	
	// Create GPT table
	// Fork addition: through runStorageCommand (fork_format.go), with the reason when it fails
	out, err := runStorageCommand("parted", "-s", diskPath, "mklabel", "gpt")
	outputBuffer.WriteString(out)
	if err != nil {
			return &outputBuffer, commandFailure("Creating the partition table on "+diskPath, "parted", out, err)
	}

	return &outputBuffer, nil
}

func FormatDisk(diskPath string, filesystemType string) (io.Reader, error) {
	utils.Log("[STORAGE] Formatting disk " + diskPath + " with filesystem " + filesystemType)

	// Fork addition: the command for the file system, waited for, its exit status
	// and output as the reason, and refused when the device is in use; upstream
	// started mkfs without waiting and never learnt whether it failed, see fork_format.go
	return forkFormatDisk(diskPath, filesystemType)
}

func CreateSinglePartition(diskPath string) (io.Reader, error) {
	utils.Log("[STORAGE] Creating single partition for " + diskPath)
	
	// Fork addition: in use means mounted, a partition mounted, swap, RAID or LVM, see fork_format.go
	if reason, err := diskInUse(diskPath); err != nil {
			return nil, err
	} else if reason != "" {
			return nil, errors.New(reason)
	}

	var outputBuffer bytes.Buffer

	// Create partition using parted
	// -s means script mode (no prompts)
	// mkpart creates a partition spanning 0% to 100% of disk
	// Fork addition: through runStorageCommand, with the reason when it fails
	out, err := runStorageCommand("parted", "-s", diskPath, "mkpart", "primary", "0%", "100%")
	outputBuffer.WriteString(out)
	if err != nil {
			return &outputBuffer, commandFailure("Creating the partition on "+diskPath, "parted", out, err)
	}

	// Force kernel to reread partition table
	// Fork addition: what partprobe said when it failed is shown; the caller waits for the partition
	if said := forkPartitionProbe(diskPath); said != "" {
		outputBuffer.WriteString("partprobe: " + said + "\n")
	}
	
	return &outputBuffer, nil
}
