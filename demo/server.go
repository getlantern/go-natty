package main

import (
	"log"
	"net"
	"sync"

	"github.com/getlantern/go-natty/natty"
	"github.com/getlantern/waddell"
)

type peer struct {
	id            waddell.PeerId
	sessions      map[uint32]*natty.Natty
	sessionsMutex sync.Mutex
}

var (
	peers      map[waddell.PeerId]*peer
	peersMutex sync.Mutex
)

func runServer() {
	log.Printf("Starting server, waddell id is \"%s\"", wc.ID().String())

	peers = make(map[waddell.PeerId]*peer)

	b := make([]byte, MAX_MESSAGE_SIZE+waddell.WADDELL_OVERHEAD)
	for {
		wm, err := wc.Receive(b)
		if err != nil {
			log.Fatalf("Unable to read message from waddell: %s", err)
		}
		answer(wm)
	}
}

func answer(wm *waddell.Message) {
	peersMutex.Lock()
	defer peersMutex.Unlock()
	p := peers[wm.From]
	if p == nil {
		p = &peer{
			id:       wm.From,
			sessions: make(map[uint32]*natty.Natty),
		}
		peers[wm.From] = p
	}
	p.answer(wm)
}

func (p *peer) answer(wm *waddell.Message) {
	p.sessionsMutex.Lock()
	defer p.sessionsMutex.Unlock()
	msg := message(wm.Body)
	sessionId := msg.getSessionID()
	nt := p.sessions[sessionId]
	if nt == nil {
		if *debug {
			log.Printf("Creating new natty")
		}
		// Set up a new Natty session
		nt = natty.Answer(debugOut)
		go func() {
			// Send
			for {
				msgOut, done := nt.NextMsgOut()
				if done {
					return
				}
				if *debug {
					log.Printf("Sending %s", msgOut)
				}
				wc.SendPieces(p.id, idToBytes(sessionId), []byte(msgOut))
			}
		}()

		go func() {
			// Receive
			defer func() {
				p.sessionsMutex.Lock()
				defer p.sessionsMutex.Unlock()
				delete(p.sessions, sessionId)
			}()

			ft, err := nt.FiveTuple()
			if err != nil {
				log.Printf("Unable to answer session %d: %s", sessionId, err)
			}

			log.Printf("Got five tuple: %s", ft)
			go readUDP(p.id, sessionId, ft)
		}()
		p.sessions[sessionId] = nt
	}
	if *debug {
		log.Printf("Received: %s", msg.getData())
	}
	nt.MsgIn(string(msg.getData()))
}

func readUDP(peerId waddell.PeerId, sessionId uint32, ft *natty.FiveTuple) {
	local, _, err := udpAddresses(ft)
	if err != nil {
		log.Fatalf("Unable to resolve UDP addresses: %s", err)
	}
	conn, err := net.ListenUDP("udp", local)
	if err != nil {
		log.Fatalf("Unable to listen on UDP: %s", err)
	}
	log.Printf("Listening for UDP packets at: %s", local)
	notifyClientOfServerReady(peerId, sessionId)
	b := make([]byte, 1024)
	for {
		n, addr, err := conn.ReadFrom(b)
		if err != nil {
			log.Fatalf("Unable to read from UDP: %s", err)
		}
		msg := string(b[:n])
		log.Printf("Got UDP message from %s: '%s'", addr, msg)
	}
}

func notifyClientOfServerReady(peerId waddell.PeerId, sessionId uint32) {
	wc.SendPieces(peerId, idToBytes(sessionId), []byte(READY))
}
