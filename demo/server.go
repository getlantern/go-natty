package main

import (
	"log"
	"strconv"
	"strings"
	"sync"

	"github.com/getlantern/go-natty/natty"
	"github.com/getlantern/go-udtrelay/udtrelay"
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
	log.Printf("Starting server, waddell id is %s ...", wc.ID().String())

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
		// Set up a new Natty session
		nt = natty.NewNatty(
			func(msgOut []byte) {
				wc.SendPieces(p.id, idToBytes(sessionId), msgOut)
			},
			nil)
		go func() {
			defer func() {
				p.sessionsMutex.Lock()
				defer p.sessionsMutex.Unlock()
				delete(p.sessions, sessionId)
			}()

			ft, err := nt.Answer()
			if err != nil {
				log.Printf("Unable to answer session %d: %s", sessionId, err)
			}

			log.Printf("Got five tuple: %s", ft)
			go runUDTServer(ft)
		}()
		p.sessions[sessionId] = nt
	}
	nt.Receive(msg.getData())
}

func runUDTServer(ft *natty.FiveTuple) {
	port, err := strconv.Atoi(strings.Split(ft.Local, ":")[1])
	if err != nil {
		log.Fatalf("Unable to extract local port from address %s: %s", ft.Local, err)
	}
	udtServer := &udtrelay.Server{
		Port:     port,
		PeerAddr: ft.Remote,
		//DebugOut: os.Stderr,
	}
	err = udtServer.Run()
	if err != nil {
		log.Fatalf("Server error: %s", err)
	}
}
