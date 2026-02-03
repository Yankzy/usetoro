package main

import (
	"fmt"
	"log"
	"os"

	"github.com/nats-io/nats.go"
)

func main() {
	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		natsURL = nats.DefaultURL
	}

	nc, err := nats.Connect(natsURL)
	if err != nil {
		log.Fatal(err)
	}
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		log.Fatal(err)
	}

	// Topic: cmd.sync.fetch
	msg := nats.NewMsg("cmd.sync.fetch")
	msg.Header.Set("Provider", "qbo")
	msg.Header.Set("Tenant-ID", "tenant-test-identity")

	_, err = js.PublishMsg(msg)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("🚀 Published sync command to NATS (Subject: cmd.sync.fetch)")
}
