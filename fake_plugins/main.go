package main

import (
	"fmt"
	"os"
)

func main() {
	content := os.Getenv("FAKE_IPAM_RESPONSE")
	if content == "" {
		content = `{ "ip4": { "ip": "1.1.1.1/32", "gateway": "", "routes": [ { "dst": "0.0.0.0/0" } ] } }`
	}

	fmt.Printf(content)
}
