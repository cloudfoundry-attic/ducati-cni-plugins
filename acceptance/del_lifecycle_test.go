package acceptance_test

import (
	"net/http"
	"os/exec"
	"regexp"

	"github.com/onsi/gomega/gexec"
	"github.com/onsi/gomega/ghttp"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("VXLAN DEL", func() {
	var (
		netConfig Config
		session   *gexec.Session
		server    *ghttp.Server

		containerID     string
		containerNSPath string
	)

	BeforeEach(func() {
		containerNSPath = "/some/container/namespace/path"

		containerID = "guid-1"

		server = ghttp.NewServer()
		serverURL := server.URL()

		netConfig = Config{
			Name:          "test-network",
			Type:          "vxlan",
			NetworkID:     "some-network-id",
			DaemonBaseURL: serverURL,
		}

		server.RouteToHandler("DELETE", regexp.MustCompile("/containers/.*"), func(resp http.ResponseWriter, req *http.Request) {
			resp.WriteHeader(http.StatusNoContent)
		})

		server.RouteToHandler("DELETE", "/networks/some-network-id/guid-1", ghttp.CombineHandlers(
			ghttp.VerifyHeaderKV("Content-Type", "application/json"),
			ghttp.RespondWith(http.StatusNoContent, ""),
		))
	})

	AfterEach(func() {
		server.Close()
	})

	Context("when delete goes smoothly", func() {
		It("removes the container info from the ducati daemon", func() {
			var err error
			var cmd *exec.Cmd
			cmd, err = buildCNICmdLight("DEL", netConfig, containerNSPath, containerID)
			Expect(err).NotTo(HaveOccurred())
			session, err = gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred())
			Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(0))

			Expect(server.ReceivedRequests()).Should(HaveLen(2))
		})
	})

	Context("when the daemon client fails", func() {
		It("returns an error", func() {
			var err error
			var cmd *exec.Cmd

			server.RouteToHandler("DELETE", "/networks/some-network-id/guid-1", ghttp.CombineHandlers(
				ghttp.VerifyHeaderKV("Content-Type", "application/json"),
				ghttp.RespondWith(http.StatusTeapot, ""),
			))

			cmd, err = buildCNICmdLight("DEL", netConfig, containerNSPath, containerID)
			Expect(err).NotTo(HaveOccurred())
			session, err = gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred())
			Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(1))
			Expect(session.Out.Contents()).To(ContainSubstring("unexpected status code on ContainerDown"))
		})
	})
})
