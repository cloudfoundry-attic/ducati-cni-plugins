package acceptance_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

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
	VXLAN string `json:"vxlan"`
}

var _ = SynchronizedBeforeSuite(
	func() []byte {
		vxlan, err := gexec.Build("github.com/cloudfoundry-incubator/ducati-cni-plugins/cmd/vxlan", "-race")
		Expect(err).NotTo(HaveOccurred())

		result, err := json.Marshal(paths{
			VXLAN: vxlan,
		})
		Expect(err).NotTo(HaveOccurred())

		return result
	},
	func(result []byte) {
		var paths paths
		err := json.Unmarshal(result, &paths)
		Expect(err).NotTo(HaveOccurred())

		cniPath = filepath.Dir(paths.VXLAN)
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
	containerID string,
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
