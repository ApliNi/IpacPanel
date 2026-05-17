package main

import (
	"log"
	"os"
)

func initProcessLogger(role string) {
	log.SetOutput(os.Stderr)
	log.SetFlags(log.LstdFlags)
	log.SetPrefix("[" + role + "] ")
}
