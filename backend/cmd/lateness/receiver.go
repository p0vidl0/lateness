package main

import (
	"os"

	"github.com/p0vidl0/lateness/internal/outletreceiver"
)

func receiver() error {
	addr := os.Getenv("LATENESS_RECEIVER_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	return outletreceiver.New().ListenAndServe(addr)
}
