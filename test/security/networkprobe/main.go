// Command networkprobe tests TCP reachability without sending credentials or
// application payloads. It is a test artifact, not part of either service image.
package main

import (
	"encoding/json"
	"net"
	"os"
	"time"
)

type result struct {
	Address   string `json:"address"`
	Connected bool   `json:"connected"`
}

func probe(address string) result {
	r := result{Address: address}
	c, err := net.DialTimeout("tcp", address, 2*time.Second)
	if err == nil {
		r.Connected = true
		_ = c.Close()
	}
	return r
}

func main() {
	if len(os.Args) != 2 {
		os.Exit(2)
	}
	if _, _, err := net.SplitHostPort(os.Args[1]); err != nil {
		os.Exit(2)
	}
	if err := json.NewEncoder(os.Stdout).Encode(probe(os.Args[1])); err != nil {
		os.Exit(1)
	}
}
