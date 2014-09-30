package main

import (
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net"
	"time"

	"github.com/getlantern/go-natty/natty"
	"github.com/getlantern/waddell"
)

var (
	server    = flag.String("server", "", "Server id (only used when running as a client)")
	socksPort = flag.Int("socksport", 18000, "Port for SOCKS server, default 18000 (only used when running as a client)")

	serverReady = make(chan bool, 10)
)

func runClient() {
	if *server == "" {
		log.Printf("Please specify a -server id")
		flag.Usage()
		return
	}
	log.Printf("Starting client, connecting to server %s ...", *server)
	traversalId := uint32(rand.Int31())
	log.Printf("Starting traversal: %d", traversalId)
	serverId, err := waddell.PeerIdFromString(*server)
	if err != nil {
		log.Fatalf("Unable to parse PeerID for server %s: %s", *server, err)
	}

	t := natty.Offer(debugOut)

	go sendMessages(t, serverId, traversalId)
	go receiveMessages(t, traversalId)

	ft, err := t.FiveTupleTimeout(TIMEOUT)
	if err != nil {
		log.Fatalf("Unable to offer: %s", err)
	}
	log.Printf("Got five tuple: %s", ft)
	if <-serverReady {
		writeUDP(ft)
	}
}

func sendMessages(t *natty.Traversal, serverId waddell.PeerId, traversalId uint32) {
	for {
		msgOut, done := t.NextMsgOut()
		if done {
			return
		}
		log.Printf("Sending %s", msgOut)
		wc.SendPieces(serverId, idToBytes(traversalId), []byte(msgOut))
	}
}

func receiveMessages(t *natty.Traversal, traversalId uint32) {
	b := make([]byte, MAX_MESSAGE_SIZE+waddell.WADDELL_OVERHEAD)
	for {
		wm, err := wc.Receive(b)
		if err != nil {
			log.Fatalf("Unable to read message from waddell: %s", err)
		}
		msg := message(wm.Body)
		if msg.getTraversalId() != traversalId {
			log.Printf("Got message for unknown traversal %d, skipping", msg.getTraversalId())
			continue
		}
		log.Printf("Received: %s", msg.getData())
		msgString := string(msg.getData())
		if READY == msgString {
			// Server's ready!
			serverReady <- true
		} else {
			t.MsgIn(msgString)
		}
	}
}

func writeUDP(ft *natty.FiveTuple) {
	local, remote, err := udpAddresses(ft)
	if err != nil {
		log.Fatalf("Unable to resolve UDP addresses: %s", err)
	}
	conn, err := net.DialUDP("udp", local, remote)
	if err != nil {
		log.Fatalf("Unable to dial UDP: %s", err)
	}
	for {
		msg := fmt.Sprintf("Hello from %s to %s", ft.Local, ft.Remote)
		log.Printf("Sending UDP message: %s", msg)
		_, err := conn.Write([]byte(msg))
		if err != nil {
			log.Fatalf("Offerer unable to write to UDP: %s", err)
		}
		time.Sleep(1 * time.Second)
	}
}
