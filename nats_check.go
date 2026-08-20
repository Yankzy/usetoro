package main

import (
	"fmt"
	"log"

	"github.com/nats-io/nats.go"
)

func main() {
	nc, err := nats.Connect("nats://localhost:4222")
	if err != nil {
		log.Fatal(err)
	}
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		log.Fatal(err)
	}

	info, err := js.ConsumerInfo("WORKFLOWS", "worker-inbox-email-postmark_inbound-durable")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Unprocessed messages: %d\n", info.NumPending)
	fmt.Printf("Redelivered: %d\n", info.NumRedelivered)
	fmt.Printf("Ack Pending: %d\n", info.NumAckPending)
}
