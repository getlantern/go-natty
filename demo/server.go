package main

import (
	"log"
	"net"
	"sync"
     "encoding/hex"
	"github.com/getlantern/go-natty/natty"
	"github.com/getlantern/waddell"
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
			id:         wm.From,
			traversals: make(map[uint32]*natty.Traversal),
		}
		peers[wm.From] = p
	}
	p.answer(wm)
}

func (p *peer) answer(wm *waddell.Message) {
	p.traversalsMutex.Lock()
	defer p.traversalsMutex.Unlock()
	msg := message(wm.Body)
	traversalId := msg.getTraversalId()
	t := p.traversals[traversalId]
	if t == nil {
		log.Printf("Answering traversal: %d", traversalId)
		// Set up a new Natty traversal
		t = natty.Answer()
		go func() {
			// Send
			for {
				msgOut, done := t.NextMsgOut()
				if done {
					return
				}
				log.Printf("Sending %s", msgOut)
				wc.SendPieces(p.id, idToBytes(traversalId), []byte(msgOut))
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

			ft, err := t.FiveTupleTimeout(TIMEOUT)
			if err != nil {
				log.Printf("Unable to answer traversal %d: %s", traversalId, err)
				return
			}

			log.Printf("Got five tuple: %s", ft)
                        if ft.Proto == "tcp" {
                           go readTCP(p.id, traversalId, ft)
                        } else if ft.Proto == "udp" {
                        go readUDP(p.id, traversalId, ft)
                        }
		}()
		p.traversals[traversalId] = t
	}
	log.Printf("Received for traversal %d: %s", traversalId, msg.getData())
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
	log.Printf("Listening for UDP packets at: %s", local)
	notifyClientOfServerReady(peerId, traversalId)
	b := make([]byte, 1024)
	for {
		n, addr, err := conn.ReadFrom(b)
		if err != nil {
			log.Fatalf("Unable to read from UDP: %s", err)
		}
		msg := hex.Dump(b[:n])
		log.Printf("Got UDP message from %s: \n%s", addr, msg)
	}
}

func readTCP(peerId waddell.PeerId, traversalId uint32, ft *natty.FiveTuple) {
        local, err := net.ResolveTCPAddr("tcp", ft.Local)
        if err != nil {
            log.Fatalf("Unknown TCP addr: %s", err)
        }
        tcplisten, err := net.ListenTCP("tcp", local)
        if err != nil {
                log.Fatalf("Unable to listen on TCP: %s", err)
        }
        log.Printf("Listening for TCP packets at: %s", local)
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
                log.Printf("Got TCP message from %s: \n%s", addr, msg)
        }
}

func notifyClientOfServerReady(peerId waddell.PeerId, traversalId uint32) {
	wc.SendPieces(peerId, idToBytes(traversalId), []byte(READY))
}
