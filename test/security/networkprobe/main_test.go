package main

import (
	"net"
	"testing"
)

func TestProbeDistinguishesListeningAndClosedSocket(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if !probe(address).Connected {
		t.Fatal("reachable control rejected")
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if probe(address).Connected {
		t.Fatal("closed endpoint reported reachable")
	}
}
