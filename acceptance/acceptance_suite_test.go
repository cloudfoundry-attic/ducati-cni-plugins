package acceptance_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"

	"testing"
)

func TestVxlan(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Vxlan Acceptance Suite")
}

var pathToVxlan, cniPath string

type paths struct {
	VXLAN    string `json:"vxlan"`
	CNI      string `json:"cni"`
	FAKEIPAM string `json:"fake_ipam"`
}

var _ = SynchronizedBeforeSuite(
	func() []byte {
		if runtime.GOOS != "linux" {
			Skip("Cannot run suite for non linux platform: " + runtime.GOOS)
		}

		cniDir := filepath.Join(os.Getenv("GOPATH"), "src", "github.com", "appc", "cni")
		cmd := exec.Command("/bin/sh", filepath.Join(cniDir, "build"))
		cmd.Dir = cniDir
		err := cmd.Run()
		Expect(err).NotTo(HaveOccurred())

		vxlan, err := gexec.Build("github.com/cloudfoundry-incubator/ducati-cni-plugins/cmd/vxlan", "-race")
		Expect(err).NotTo(HaveOccurred())

		fakeIpam, err := gexec.Build("github.com/cloudfoundry-incubator/ducati-cni-plugins/fake_plugins", "-race")
		Expect(err).NotTo(HaveOccurred())

		result, err := json.Marshal(paths{
			VXLAN:    vxlan,
			CNI:      cniDir,
			FAKEIPAM: fakeIpam,
		})
		Expect(err).NotTo(HaveOccurred())

		return result
	},
	func(result []byte) {
		var paths paths
		err := json.Unmarshal(result, &paths)
		Expect(err).NotTo(HaveOccurred())

		cniBinDir := filepath.Join(paths.CNI, "bin")
		vxlanBinDir := filepath.Dir(paths.VXLAN)
		fakeIpamDir := filepath.Dir(paths.FAKEIPAM)

		cniPath = fmt.Sprintf("%s%c%s%c%s", vxlanBinDir, os.PathListSeparator, cniBinDir, os.PathListSeparator, fakeIpamDir)
		pathToVxlan = paths.VXLAN
	},
)

var _ = SynchronizedAfterSuite(func() {
	return
}, func() {
	gexec.CleanupBuildArtifacts()
})

func buildCNICmdLight(
	operation string,
	netConfig Config,
	containerNSPath string,
	containerID, sandboxRepoDir, serverURL string,
) (*exec.Cmd, error) {

	input, err := json.Marshal(netConfig)
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(pathToVxlan)
	cmd.Stdin = bytes.NewReader(input)
	cmd.Env = append(
		os.Environ(),
		fmt.Sprintf("CNI_COMMAND=%s", operation),
		fmt.Sprintf("CNI_CONTAINERID=%s", containerID),
		fmt.Sprintf("CNI_PATH=%s", cniPath),
		fmt.Sprintf("CNI_NETNS=%s", containerNSPath),
		fmt.Sprintf("CNI_IFNAME=%s", "vx-eth0"),
	)

	return cmd, nil
}
