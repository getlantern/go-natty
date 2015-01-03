package main

import (
	"encoding/hex"
	"github.com/getlantern/go-natty/natty"
	"github.com/getlantern/waddell"
	"net"
	"sync"
)

type peer struct {
	id              waddell.PeerId
	traversals      map[uint32]*natty.Traversal
	traversalsMutex sync.Mutex
}

var (
	peers      map[waddell.PeerId]*peer
	peersMutex sync.Mutex
)

func runServer() {
	log.Debugf("Starting server, waddell id is \"%s\"", id.String())

	peers = make(map[waddell.PeerId]*peer)

	for wm := range in {
		answer(wm)
	}
}

func answer(wm *waddell.MessageIn) {
	peersMutex.Lock()
	defer peersMutex.Unlock()
	p := peers[wm.From]
	if p == nil {
		p = &peer{
			id:         wm.From,
			traversals: make(map[uint32]*natty.Traversal),
		}
		peers[wm.From] = p
	}
	p.answer(wm)
}

func (p *peer) answer(wm *waddell.MessageIn) {
	p.traversalsMutex.Lock()
	defer p.traversalsMutex.Unlock()
	msg := message(wm.Body)
	traversalId := msg.getTraversalId()
	t := p.traversals[traversalId]
	if t == nil {
		log.Debugf("Answering traversal: %d", traversalId)
		// Set up a new Natty traversal
		t = natty.Answer(TIMEOUT)
		go func() {
			// Send
			for {
				msgOut, done := t.NextMsgOut()
				if done {
					return
				}
				log.Debugf("Sending %s", msgOut)
				out <- waddell.Message(p.id, idToBytes(traversalId), []byte(msgOut))
			}
		}()

		go func() {
			// Receive
			defer func() {
				p.traversalsMutex.Lock()
				defer p.traversalsMutex.Unlock()
				delete(p.traversals, traversalId)
				t.Close()
			}()

			ft, err := t.FiveTuple()
			if err != nil {
				log.Debugf("Unable to answer traversal %d: %s", traversalId, err)
				return
			}

			log.Debugf("Got five tuple: %s", ft)
			if ft.Proto == "tcp" {
				go readTCP(p.id, traversalId, ft)
			} else if ft.Proto == "udp" {
				go readUDP(p.id, traversalId, ft)
			}
		}()
		p.traversals[traversalId] = t
	}
	log.Debugf("Received for traversal %d: %s", traversalId, msg.getData())
	t.MsgIn(string(msg.getData()))
}

func readUDP(peerId waddell.PeerId, traversalId uint32, ft *natty.FiveTuple) {
	local, _, err := ft.UDPAddrs()
	if err != nil {
		log.Fatalf("Unable to resolve UDP addresses: %s", err)
	}
	conn, err := net.ListenUDP("udp", local)
	if err != nil {
		log.Fatalf("Unable to listen on UDP: %s", err)
	}
	log.Debugf("Listening for UDP packets at: %s", local)
	notifyClientOfServerReady(peerId, traversalId)
	b := make([]byte, 1024)
	for {
		n, addr, err := conn.ReadFrom(b)
		if err != nil {
			log.Fatalf("Unable to read from UDP: %s", err)
		}
		msg := hex.Dump(b[:n])
		log.Debugf("Got UDP message from %s: \n%s", addr, msg)
	}
}

func readTCP(peerId waddell.PeerId, traversalId uint32, ft *natty.FiveTuple) {
	local, _, err := ft.TCPAddrs()
	if err != nil {
		log.Fatalf("Unknown TCP addr: %s", err)
	}
	tcplisten, err := net.ListenTCP("tcp", local)
	if err != nil {
		log.Fatalf("Unable to listen on TCP: %s", err)
	}
	log.Debugf("Listening for TCP packets at: %s", local)
	notifyClientOfServerReady(peerId, traversalId)
	b := make([]byte, 1024)
	conn, err := tcplisten.Accept()
	if err != nil {
		log.Fatalf("Unable to accept on TCP: %s", err)
	}
	addr := conn.RemoteAddr()
	for {
		n, err := conn.Read(b)
		if err != nil {
			log.Fatalf("Unable to read from TCP: %s", err)
		}
		msg := hex.Dump(b[:n])
		log.Debugf("Got TCP message from %s: \n%s", addr, msg)
	}
}

func notifyClientOfServerReady(peerId waddell.PeerId, traversalId uint32) {
	out <- waddell.Message(peerId, idToBytes(traversalId), []byte(READY))
}
