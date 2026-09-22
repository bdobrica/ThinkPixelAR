package main

import (
	"strings"
	"testing"
)

func TestRestrictedStatus(t *testing.T) {
	safe := "Uid:\t65532\t65532\t65532\t65532\nGid:\t65532\t65532\t65532\t65532\nCapInh:\t0000000000000000\nCapPrm:\t0000000000000000\nCapEff:\t0000000000000000\nCapBnd:\t0000000000000000\nCapAmb:\t0000000000000000\nNoNewPrivs:\t1\nSeccomp:\t2\n"
	if !restrictedStatus(safe) {
		t.Fatal("restricted process rejected")
	}
	for _, key := range []string{"Uid", "Gid", "CapInh", "CapPrm", "CapEff", "CapBnd", "CapAmb", "NoNewPrivs", "Seccomp"} {
		t.Run(key, func(t *testing.T) {
			lines := strings.Split(safe, "\n")
			for i, line := range lines {
				if strings.HasPrefix(line, key+":") {
					lines[i] = key + ":\t0"
					if strings.HasPrefix(key, "Cap") {
						lines[i] = key + ":\t0000000000200000"
					}
				}
			}
			if restrictedStatus(strings.Join(lines, "\n")) {
				t.Fatal("unsafe process accepted")
			}
			for i, line := range lines {
				if strings.HasPrefix(line, key+":") {
					lines[i] = ""
				}
			}
			if restrictedStatus(strings.Join(lines, "\n")) {
				t.Fatal("missing evidence accepted")
			}
		})
	}
}
