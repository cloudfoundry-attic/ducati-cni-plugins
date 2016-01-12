package main_test

import (
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
	RunSpecs(t, "Vxlan Suite")
}

var pathToVxlan, cniPath string

type paths struct {
	VXLAN string `json:"vxlan"`
	CNI   string `json:"cni"`
}

var _ = SynchronizedBeforeSuite(
	func() []byte {
		if runtime.GOOS != "linux" {
			Skip("Cannot run suite for non linux platform: " + runtime.GOOS)
		}

		wd, err := os.Getwd()
		Expect(err).NotTo(HaveOccurred())

		cniDir := filepath.Join(wd, "../../../../appc/cni")
		cmd := exec.Command("/bin/sh", filepath.Join(cniDir, "build"))
		cmd.Dir = cniDir
		err = cmd.Run()
		Expect(err).NotTo(HaveOccurred())

		// race detector doesn't work with cgo in go 1.5
		vxlan, err := gexec.Build("github.com/cloudfoundry-incubator/ducati-cni-plugins/cmd/vxlan")
		Expect(err).NotTo(HaveOccurred())

		result, err := json.Marshal(paths{
			VXLAN: vxlan,
			CNI:   cniDir,
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

		cniPath = fmt.Sprintf("%s%c%s", vxlanBinDir, os.PathListSeparator, cniBinDir)
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
