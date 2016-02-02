package acceptance_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/namespace"
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

		// race detector doesn't work with cgo in go 1.5
		vxlan, err := gexec.Build("github.com/cloudfoundry-incubator/ducati-cni-plugins/cmd/vxlan")
		Expect(err).NotTo(HaveOccurred())

		fakeIpam, err := gexec.Build("github.com/cloudfoundry-incubator/ducati-cni-plugins/fake_plugins")
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

func newNetworkNamespace(name string) string {
	namespace := "/var/run/netns/" + name
	cmd := exec.Command("ip", "netns", "add", name)
	err := cmd.Run()
	Expect(err).NotTo(HaveOccurred())

	Expect(namespace).To(BeAnExistingFile())
	return namespace
}

func removeNetworkNamespace(name string) {
	cmd := exec.Command("ip", "netns", "delete", name)
	err := cmd.Run()
	Expect(err).NotTo(HaveOccurred())
}

var execCNI = func(operation string, netConfig Config, containerNS namespace.Namespace,
	containerID, sandboxRepoDir string) (namespace.Namespace, *gexec.Session) {

	input, err := json.Marshal(netConfig)
	Expect(err).NotTo(HaveOccurred())

	cmd := exec.Command(pathToVxlan)
	cmd.Stdin = bytes.NewReader(input)
	cmd.Env = append(
		os.Environ(),
		fmt.Sprintf("CNI_COMMAND=%s", operation),
		fmt.Sprintf("CNI_CONTAINERID=%s", containerID),
		fmt.Sprintf("CNI_PATH=%s", cniPath),
		fmt.Sprintf("CNI_NETNS=%s", containerNS.Path()),
		fmt.Sprintf("CNI_IFNAME=%s", "vx-eth0"),
		fmt.Sprintf("DUCATI_OS_SANDBOX_REPO=%s", sandboxRepoDir),
	)

	session, err := gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
	Expect(err).NotTo(HaveOccurred())

	return namespace.NewNamespace(filepath.Join(sandboxRepoDir, fmt.Sprintf("vni-%d", vni))), session
}
