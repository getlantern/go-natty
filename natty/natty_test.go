package natty

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/getlantern/waddell"
)

const (
	MESSAGE_TEXT = "Hello World"

	WADDELL_ADDR = "localhost:19543"
)

// TestDirect starts up two local Natty instances that communicate with each
// other directly.  Once connected, one Natty sends a UDP packet to the other
// to make sure that the connection works.
func TestDirect(t *testing.T) {
	doTest(t, func(offerer *Natty, answerer *Natty) {
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
	})
}

// TestWaddell starts up two local Natty instances that communicate with each
// other using waddell.  Once connected, one Natty sends a UDP packet to the
// other to make sure that the connection works.
func TestWaddell(t *testing.T) {
	doTest(t, func(offerer *Natty, answerer *Natty) {
		// Start a waddell server
		server := &waddell.Server{}
		log.Printf("Starting waddell at %s", WADDELL_ADDR)
		listener, err := net.Listen("tcp", WADDELL_ADDR)
		if err != nil {
			t.Fatalf("Unable to listen at %s: %s", WADDELL_ADDR, err)
		}
		go func() {
			err = server.Serve(listener)
			if err != nil {
				t.Fatalf("Unable to start waddell at %s: %s", WADDELL_ADDR, err)
			}
		}()

		offererClient := makeWaddellClient(t)
		answererClient := makeWaddellClient(t)

		// Send from Offerer -> Answerer
		go func() {
			for {
				msg, done := offerer.NextMsgOut()
				if done {
					return
				}
				log.Printf("Offerer -> Answerer: %s", msg)
				offererClient.Send(answererClient.ID(), []byte(msg))
			}
		}()

		// Receive to Offerer
		go func() {
			for {
				b := make([]byte, 4096+waddell.WADDELL_OVERHEAD)
				msg, err := offererClient.Receive(b)
				if err != nil {
					t.Fatalf("Offerer unable to receive message from waddell: %s", err)
				}
				offerer.MsgIn(string(msg.Body))
			}
		}()

		// Send from Answerer -> Offerer
		go func() {
			for {
				msg, done := answerer.NextMsgOut()
				if done {
					return
				}
				log.Printf("Answerer -> Offerer: %s", msg)
				answererClient.Send(offererClient.ID(), []byte(msg))
			}
		}()

		// Receive to Ansserer
		go func() {
			for {
				b := make([]byte, 4096+waddell.WADDELL_OVERHEAD)
				msg, err := answererClient.Receive(b)
				if err != nil {
					t.Fatalf("Answerer unable to receive message from waddell: %s", err)
				}
				answerer.MsgIn(string(msg.Body))
			}
		}()

	})
}

func doTest(t *testing.T, signal func(*Natty, *Natty)) {
	var offerer *Natty
	var answerer *Natty

	var debug io.Writer
	if testing.Verbose() {
		debug = os.Stderr
	}
	offerer = Offer(debug)
	answerer = Answer(debug)

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

	signal(offerer, answerer)

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

func makeWaddellClient(t *testing.T) *waddell.Client {
	conn, err := net.Dial("tcp", WADDELL_ADDR)
	if err != nil {
		t.Fatalf("Unable to dial waddell: %s", err)
	}
	wc, err := waddell.Connect(conn)
	if err != nil {
		t.Fatalf("Unable to connect to waddell: %s", err)
	}
	return wc
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
