package namespace_test

//func getInode(path string) uint64 {
//var stat syscall.Stat_t
//if err := syscall.Stat(path, &stat); err != nil {
//panic(fmt.Errorf("unable to get inode: %s", err))
//}
//return stat.Ino
//}

//var _ = Describe("Namespace", func() {
//Describe("Name", func() {
//It("returns the basename of the underlying path", func() {
//ns := &namespace.Namespace{Path: "/var/run/netns/foo"}
//Expect(ns.Name()).To(Equal("foo"))

//ns = &namespace.Namespace{Path: "/foo"}
//Expect(ns.Name()).To(Equal("foo"))

//ns = &namespace.Namespace{Path: "/foo/bar"}
//Expect(ns.Name()).To(Equal("bar"))
//})
//})

//Describe("Execute", func() {
//var nsInode uint64

//BeforeEach(func() {
//fmt.Printf("host's view of it's own NS: %d\n", getInode("/proc/self/ns/net"))
//err := exec.Command("ip", "netns", "add", "ns-test-ns").Run()
//Expect(err).NotTo(HaveOccurred())

//var stat syscall.Stat_t
//err = syscall.Stat("/var/run/netns/ns-test-ns", &stat)
//Expect(err).NotTo(HaveOccurred())

//nsInode = stat.Ino
//fmt.Printf("container's NS (as seen by host): %d\n", getInode("/var/run/netns/ns-test-ns"))
//})

//AfterEach(func() {
//err := exec.Command("ip", "netns", "delete", "ns-test-ns").Run()
//Expect(err).NotTo(HaveOccurred())
//})

//It("runs the closure in the namespace", func() {

//ns := &namespace.Namespace{Path: "/var/run/netns/ns-test-ns"}

//var stat syscall.Stat_t
//closure := func() error {
//fmt.Printf("container NS (as seen by itself): %d\n", getInode("/proc/self/ns/net"))
//return syscall.Stat("/proc/self/ns/net", &stat)
//}

//err := ns.Execute(closure)
//Expect(err).NotTo(HaveOccurred())
//Expect(stat.Ino).To(Equal(nsInode))

//fmt.Printf("host NS (seen by itself): %d\n", getInode("/proc/self/ns/net"))
//})
//})
//})
