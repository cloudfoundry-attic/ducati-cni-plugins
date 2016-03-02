package acceptance_test

import (
	"encoding/json"
	"io/ioutil"
	"net"
	"net/http"
	"os/exec"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/appc/cni/pkg/types"
	"github.com/onsi/gomega/gexec"
	"github.com/onsi/gomega/ghttp"
)

type IPAM struct {
	Type   string              `json:"type,omitempty"`
	Subnet string              `json:"subnet,omitempty"`
	Routes []map[string]string `json:"routes,omitempty"`
}

type Config struct {
	Name          string `json:"name"`
	Type          string `json:"type"`
	NetworkID     string `json:"network_id"`
	DaemonBaseURL string `json:"daemon_base_url"`
}

const vni = 1
const DEFAULT_TIMEOUT = "3s"

var _ = Describe("VXLAN ADD", func() {
	var (
		netConfig Config
		session   *gexec.Session
		server    *ghttp.Server

		containerID string
		reqBytes    []byte

		containerNSPath string

		ipamResult types.Result
	)

	BeforeEach(func() {
		containerID = "guid-1"
		containerNSPath = "/some/container/namespace/path"

		server = ghttp.NewServer()
		serverURL := server.URL()

		netConfig = Config{
			Name:          "test-network",
			Type:          "vxlan",
			NetworkID:     "some-network-id",
			DaemonBaseURL: serverURL,
		}

		ipamResult = types.Result{
			IP4: &types.IPConfig{
				IP: net.IPNet{
					IP:   net.ParseIP("192.168.1.2"),
					Mask: net.CIDRMask(24, 32),
				},
				Gateway: net.ParseIP("192.168.1.1"),
				Routes: []types.Route{{
					Dst: net.IPNet{
						IP:   net.ParseIP("192.168.0.0"),
						Mask: net.CIDRMask(16, 32),
					},
					GW: net.ParseIP("192.168.1.1"),
				}},
			},
		}

		ipamStatusCode := http.StatusCreated
		ipamPostHandler := ghttp.CombineHandlers(
			ghttp.VerifyRequest("POST", "/ipam/some-network-id/guid-1"),
			ghttp.VerifyHeaderKV("Content-Type", "application/json"),
			ghttp.RespondWithJSONEncodedPtr(&ipamStatusCode, &ipamResult),
		)

		server.RouteToHandler("POST", "/ipam/some-network-id/guid-1", ipamPostHandler)
		server.RouteToHandler("POST", "/containers", func(resp http.ResponseWriter, req *http.Request) {
			var err error
			reqBytes, err = ioutil.ReadAll(req.Body)
			Expect(err).NotTo(HaveOccurred())
			resp.WriteHeader(http.StatusCreated)
		})

		server.RouteToHandler("POST", "/networks/some-network-id/guid-1", ghttp.CombineHandlers(
			ghttp.VerifyHeaderKV("Content-Type", "application/json"),
			ghttp.RespondWith(http.StatusCreated, ""),
		))
	})

	AfterEach(func() {
		server.Close()
	})

	Describe("ADD", func() {
		It("returns IPAM data", func() {
			var err error
			var cmd *exec.Cmd
			cmd, err = buildCNICmdLight("ADD", netConfig, containerNSPath, containerID)
			Expect(err).NotTo(HaveOccurred())
			session, err = gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred())
			Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(0))

			var result types.Result
			err = json.Unmarshal(session.Out.Contents(), &result)
			Expect(err).NotTo(HaveOccurred())

			Expect(result.IP4.Gateway.String()).To(Equal("192.168.1.1"))
			Expect(result.IP4.Routes).To(HaveLen(1))
			Expect(result.IP4.Routes[0].Dst.String()).To(Equal("192.168.0.0/16"))
		})

		Context("when the call to allocate an IP fails", func() {
			BeforeEach(func() {
				server.RouteToHandler("POST", "/ipam/some-network-id/guid-1", ghttp.RespondWith(http.StatusInternalServerError, nil))
			})

			It("returns an error", func() {
				var err error
				var cmd *exec.Cmd
				cmd, err = buildCNICmdLight("ADD", netConfig, containerNSPath, containerID)
				Expect(err).NotTo(HaveOccurred())
				session, err = gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
				Expect(err).NotTo(HaveOccurred())
				Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(1))

				Expect(server.ReceivedRequests()).To(HaveLen(1))
				Expect(session.Out.Contents()).To(ContainSubstring("unexpected status code on AllocateIP"))
			})
		})

		Context("when the call to the daemon to register the container fails", func() {
			BeforeEach(func() {
				server.RouteToHandler("POST", "/networks/some-network-id/guid-1", ghttp.RespondWith(http.StatusInternalServerError, nil))
			})

			It("returns an error", func() {
				var err error
				var cmd *exec.Cmd
				cmd, err = buildCNICmdLight("ADD", netConfig, containerNSPath, containerID)
				Expect(err).NotTo(HaveOccurred())
				session, err = gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
				Expect(err).NotTo(HaveOccurred())
				Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(1))

				Expect(server.ReceivedRequests()).To(HaveLen(2))
				Expect(session.Out.Contents()).To(ContainSubstring("unexpected status code on ContainerUp: expected 201 but got 500"))
			})
		})

		Context("when CNI_CONTAINERID is not set", func() {
			BeforeEach(func() {
				containerID = ""
			})

			It("exits with an error", func() {
				var err error
				var cmd *exec.Cmd
				cmd, err = buildCNICmdLight("ADD", netConfig, containerNSPath, containerID)
				Expect(err).NotTo(HaveOccurred())
				session, err = gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
				Expect(err).NotTo(HaveOccurred())
				Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(1))

				Expect(session.Out.Contents()).To(ContainSubstring("CNI_CONTAINERID is required"))
			})
		})

		Context("when daemon_base_url is not set", func() {
			BeforeEach(func() {
				netConfig.DaemonBaseURL = ""
			})

			It("exits with an error", func() {
				var err error
				var cmd *exec.Cmd
				cmd, err = buildCNICmdLight("ADD", netConfig, containerNSPath, containerID)
				Expect(err).NotTo(HaveOccurred())
				session, err = gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
				Expect(err).NotTo(HaveOccurred())
				Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(1))

				Expect(session.Out.Contents()).To(ContainSubstring(`\"daemon_base_url\" field required.`))
			})
		})

	})
})
