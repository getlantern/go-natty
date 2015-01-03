package main

import (
	"flag"
	"fmt"
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
		log.Debugf("Please specify a -server id")
		flag.Usage()
		return
	}
	log.Debugf("Starting client, connecting to server %s ...", *server)
	traversalId := uint32(rand.Int31())
	log.Debugf("Starting traversal: %d", traversalId)
	serverId, err := waddell.PeerIdFromString(*server)
	if err != nil {
		log.Fatalf("Unable to parse PeerID for server %s: %s", *server, err)
	}

	t := natty.Offer(TIMEOUT)
	defer t.Close()

	go sendMessages(t, serverId, traversalId)
	go receiveMessages(t, traversalId)

	ft, err := t.FiveTuple()
	if err != nil {
		t.Close()
		log.Fatalf("Unable to offer: %s", err)
	}
	log.Debugf("Got five tuple: %s", ft)
	if <-serverReady {
		if "tcp" == *proto {
			writeTCP(ft)
		} else {
			writeUDP(ft)
		}
	}
}

func sendMessages(t *natty.Traversal, serverId waddell.PeerId, traversalId uint32) {
	for {
		msgOut, done := t.NextMsgOut()
		if done {
			return
		}
		log.Debugf("Sending %s", msgOut)
		out <- waddell.Message(serverId, idToBytes(traversalId), []byte(msgOut))
	}
}

func receiveMessages(t *natty.Traversal, traversalId uint32) {
	for wm := range in {
		msg := message(wm.Body)
		if msg.getTraversalId() != traversalId {
			log.Debugf("Got message for unknown traversal %d, skipping", msg.getTraversalId())
			continue
		}
		log.Debugf("Received: %s", msg.getData())
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
	local, remote, err := ft.UDPAddrs()
	if err != nil {
		log.Fatalf("Unable to resolve UDP addresses: %s", err)
	}
        local.IP = net.IPv4(0,0,0,0)
	conn, err := net.DialUDP("udp", nil, remote)
	if err != nil {
		log.Fatalf("Unable to dial UDP: %s", err)
	}
	for {
		msg := fmt.Sprintf("Hello from %s to %s", local, remote)
		log.Debugf("Sending UDP message: %s", msg)
		_, err := conn.Write([]byte(msg))
		if err != nil {
			log.Fatalf("Offerer unable to write to UDP: %s", err)
		}
		time.Sleep(1 * time.Second)
	}
}

func writeTCP(ft *natty.FiveTuple) {
	local, remote, err := ft.TCPAddrs()
	if err != nil {
		log.Fatalf("Unable to resolve TCP addresses: %s", err)
	}
	conn, err := net.DialTCP("tcp", local, remote)
	if err != nil {
		log.Fatalf("Unable to dial TCP: %s", err)
	}
	for {
		msg := fmt.Sprintf("Hello from %s to %s", ft.Local, ft.Remote)
		log.Debugf("Sending TCP message: %s", msg)
		_, err := conn.Write([]byte(msg))
		if err != nil {
			log.Fatalf("Offerer unable to write to TCP: %s", err)
		}
		time.Sleep(1 * time.Second)
	}
}
