package natty

import (
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"testing"
	"time"
)

const (
	MESSAGE_TEXT = "Hello World"
)

// TestLocal starts up two local Natty instances that communicate with each
// other directly.  Once connected, one Natty sends a UDP packet to the other
// to make sure that the connection works.
func TestLocal(t *testing.T) {
	var offerer *Natty
	var answerer *Natty

	offerer = Offer(os.Stderr)
	answerer = Answer(os.Stderr)

	var answererReady sync.WaitGroup
	answererReady.Add(1)

	var wg sync.WaitGroup
	wg.Add(2)

	// Offerer processing
	go func() {
		defer wg.Done()
		fiveTuple, err := offerer.FiveTuple()
		if err != nil {
			t.Errorf("Offerer had error: %s", err)
			return
		}
		log.Printf("Offerer got 5 tuple: %s", fiveTuple)
		if fiveTuple.Proto != UDP {
			t.Errorf("Protocol was %s instead of udp", fiveTuple.Proto)
			return
		}
		local, remote, err := udpAddresses(fiveTuple)
		if err != nil {
			t.Error("Offerer unable to resolve UDP addresses: %s", err)
			return
		}
		answererReady.Wait()
		conn, err := net.DialUDP("udp", local, remote)
		if err != nil {
			t.Errorf("Unable to dial UDP: %s", err)
			return
		}
		for i := 0; i < 10; i++ {
			_, err := conn.Write([]byte(MESSAGE_TEXT))
			if err != nil {
				t.Errorf("Offerer unable to write to UDP: %s", err)
				return
			}
		}
	}()

	// Answerer processing
	go func() {
		defer wg.Done()
		fiveTuple, err := answerer.FiveTuple()
		if err != nil {
			t.Errorf("Answerer had error: %s", err)
			return
		}
		if fiveTuple.Proto != UDP {
			t.Errorf("Protocol was %s instead of udp", fiveTuple.Proto)
			return
		}
		log.Printf("Answerer got 5 tuple: %s", fiveTuple)
		local, _, err := udpAddresses(fiveTuple)
		if err != nil {
			t.Errorf("Error in Answerer: %s", err)
			return
		}
		conn, err := net.ListenUDP("udp", local)
		if err != nil {
			t.Errorf("Answerer unable to listen on UDP: %s", err)
			return
		}
		answererReady.Done()
		b := make([]byte, 1024)
		for {
			n, addr, err := conn.ReadFrom(b)
			if err != nil {
				t.Errorf("Answerer unable to read from UDP: %s", err)
				return
			}
			if addr.String() != fiveTuple.Remote {
				t.Errorf("UDP package had address %s, expected %s", addr, fiveTuple.Remote)
				return
			}
			msg := string(b[:n])
			if msg != MESSAGE_TEXT {
				log.Printf("Got message '%s', expected '%s'", msg, MESSAGE_TEXT)
			}
			return
		}
	}()

	// "Signaling" - this would typically be done using a signaling server like
	// waddell when talking to a remote Natty
	go func() {
		for {
			msg, done := offerer.NextMsgOut()
			if done {
				return
			}
			log.Printf("Offerer -> Answerer: %s", msg)
			answerer.MsgIn(msg)
		}
	}()

	go func() {
		for {
			msg, done := answerer.NextMsgOut()
			if done {
				return
			}
			log.Printf("Answerer -> Offerer: %s", msg)
			offerer.MsgIn(msg)
		}
	}()

	doneCh := make(chan interface{})
	go func() {
		wg.Wait()
		doneCh <- nil
	}()

	select {
	case <-doneCh:
		return
	case <-time.After(1000 * time.Second):
		t.Errorf("Test timed out")
	}
}

func udpAddresses(fiveTuple *FiveTuple) (*net.UDPAddr, *net.UDPAddr, error) {
	if fiveTuple.Proto != UDP {
		return nil, nil, fmt.Errorf("FiveTuple.Proto was not UDP!: %s", fiveTuple.Proto)
	}
	local, err := net.ResolveUDPAddr("udp", fiveTuple.Local)
	if err != nil {
		return nil, nil, fmt.Errorf("Unable to resolve local UDP address %s: %s", fiveTuple.Local)
	}
	remote, err := net.ResolveUDPAddr("udp", fiveTuple.Remote)
	if err != nil {
		return nil, nil, fmt.Errorf("Unable to resolve remote UDP address %s: %s", fiveTuple.Remote)
	}
	return local, remote, nil
}
