package natty

import (
	"log"
	"net"
	"os"
	"time"
)

func ExampleOffer() {
	t := Offer(os.Stderr)
	defer t.Close()

	// Process outbound messages
	go func() {
		for {
			msg, done := t.NextMsgOut()
			if done {
				return
			}
			// TODO: Send message to peer via signaling channel
			log.Printf("Sent: %s", msg)
		}
	}()

	// Process inbound messages
	go func() {
		for {
			// TODO: Get message from signaling channel
			var msg string
			t.MsgIn(msg)
		}
	}()

	// Try it with a really short timeout (should error)
	fiveTuple, err := t.FiveTupleTimeout(15 * time.Second)
	if err != nil {
		log.Fatal(err)
	}

	local, remote, err := fiveTuple.UDPAddrs()
	if err != nil {
		log.Fatal(err)
	}

	// TODO: Wait for peer to signal that it's ready to receive traffic
	conn, err := net.DialUDP("udp", local, remote)
	if err != nil {
		log.Fatal(err)
	}
	for {
		_, err := conn.Write([]byte("My data"))
		if err != nil {
			log.Fatal(err)
		}
	}
}

func ExampleAnswer() {
	t := Answer(os.Stderr)
	defer t.Close()

	// Process outbound messages
	go func() {
		for {
			msg, done := t.NextMsgOut()
			if done {
				return
			}
			// TODO: Send message to peer via signaling channel
			log.Printf("Sent: %s", msg)
		}
	}()

	// Process inbound messages
	go func() {
		for {
			// TODO: Get message from signaling channel
			var msg string
			t.MsgIn(msg)
		}
	}()

	fiveTuple, err := t.FiveTupleTimeout(15 * time.Second)
	if err != nil {
		log.Fatal(err)
	}

	local, _, err := fiveTuple.UDPAddrs()
	if err != nil {
		log.Fatal(err)
	}

	conn, err := net.ListenUDP("udp", local)
	if err != nil {
		log.Fatal(err)
	}

	// TODO: signal to peer that we're ready to receive traffic

	b := make([]byte, 1024)
	for {
		n, addr, err := conn.ReadFrom(b)
		if err != nil {
			log.Fatal(err)
		}

		log.Printf("Received message '%s' from %s", string(b[:n]), addr)
	}
}
