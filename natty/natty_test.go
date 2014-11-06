package natty

import (
	"io"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/getlantern/golog"
	"github.com/getlantern/waddell"
)

const (
	MESSAGE_TEXT = "Hello World"

	WADDELL_ADDR = "localhost:19543"
)

var tlog = golog.LoggerFor("natty-test")

// TestDirect starts up two local Traversals that communicate with each other
// directly.  Once connected, one peer sends a UDP packet to the other to make
// sure that the connection works.
//
// Run test with -v flag to get debug output from natty.
func TestDirect(t *testing.T) {
	doTest(t, func(offer *Traversal, answer *Traversal) {
		go func() {
			for {
				msg, done := offer.NextMsgOut()
				if done {
					return
				}
				tlog.Debugf("offer -> answer: %s", msg)
				answer.MsgIn(msg)
			}
		}()

		go func() {
			for {
				msg, done := answer.NextMsgOut()
				if done {
					return
				}
				tlog.Debugf("answer -> offer: %s", msg)
				offer.MsgIn(msg)
			}
		}()
	})
}

// TestWaddell starts up two local Traversals that communicate with each other
// using a local waddell server.  Once connected, one peer sends a UDP packet to
// the other to make sure that the connection works.
//
// Run test with -v flag to get debug output from natty.
func TestWaddell(t *testing.T) {
	doTest(t, func(offer *Traversal, answer *Traversal) {
		// Start a waddell server
		server := &waddell.Server{}
		tlog.Debugf("Starting waddell at %s", WADDELL_ADDR)
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

		offerClient := makeWaddellClient(t)
		answerClient := makeWaddellClient(t)

		// Send from offer -> answer
		go func() {
			for {
				msg, done := offer.NextMsgOut()
				if done {
					return
				}
				tlog.Debugf("offer -> answer: %s", msg)
				offerClient.Send(answerClient.ID(), []byte(msg))
			}
		}()

		// Receive to offer
		go func() {
			for {
				b := make([]byte, 4096+waddell.WADDELL_OVERHEAD)
				msg, err := offerClient.Receive(b)
				if err != nil {
					t.Fatalf("offer unable to receive message from waddell: %s", err)
				}
				offer.MsgIn(string(msg.Body))
			}
		}()

		// Send from answer -> offer
		go func() {
			for {
				msg, done := answer.NextMsgOut()
				if done {
					return
				}
				tlog.Debugf("answer -> offer: %s", msg)
				answerClient.Send(offerClient.ID(), []byte(msg))
			}
		}()

		// Receive to answer
		go func() {
			for {
				b := make([]byte, 4096+waddell.WADDELL_OVERHEAD)
				msg, err := answerClient.Receive(b)
				if err != nil {
					t.Fatalf("answer unable to receive message from waddell: %s", err)
				}
				answer.MsgIn(string(msg.Body))
			}
		}()

	})
}

func doTest(t *testing.T, signal func(*Traversal, *Traversal)) {
	var offer *Traversal
	var answer *Traversal

	var debug io.Writer
	if testing.Verbose() {
		debug = os.Stderr
	}

	offer = Offer(debug)
	defer offer.Close()

	answer = Answer(debug)
	defer answer.Close()

	var answerReady sync.WaitGroup
	answerReady.Add(1)

	var wg sync.WaitGroup
	wg.Add(2)

	// offer processing
	go func() {
		defer wg.Done()
		// Try it with a really short timeout (should error)
		fiveTuple, err := offer.FiveTupleTimeout(5 * time.Millisecond)
		if err == nil {
			errorf(t, "Really short timeout should have given error")
		}

		// Try it again without timeout
		fiveTuple, err = offer.FiveTuple()
		if err != nil {
			errorf(t, "offer had error: %s", err)
			return
		}

		// Call it again to make sure we're getting the same 5-tuple
		fiveTupleAgain, err := offer.FiveTuple()
		if fiveTupleAgain.Local != fiveTuple.Local ||
			fiveTupleAgain.Remote != fiveTuple.Remote ||
			fiveTupleAgain.Proto != fiveTuple.Proto {
			errorf(t, "2nd FiveTuple didn't match original")
		}

		tlog.Debugf("offer got FiveTuple: %s", fiveTuple)
		if fiveTuple.Proto != UDP {
			errorf(t, "Protocol was %s instead of udp", fiveTuple.Proto)
			return
		}
		local, remote, err := fiveTuple.UDPAddrs()
		if err != nil {
			errorf(t, "offer unable to resolve UDP addresses: %s", err)
			return
		}
		answerReady.Wait()
		tlog.Debug("Offer got answerReady")
		conn, err := net.DialUDP("udp", local, remote)
		if err != nil {
			errorf(t, "Unable to dial UDP: %s", err)
			return
		}
		tlog.Debugf("Offer connected to %s, sending data", local)
		for i := 0; i < 10; i++ {
			_, err := conn.Write([]byte(MESSAGE_TEXT))
			if err != nil {
				errorf(t, "offer unable to write to UDP: %s", err)
				return
			}
		}
		tlog.Debug("Offer done sending data")
	}()

	// answer processing
	go func() {
		defer wg.Done()
		fiveTuple, err := answer.FiveTupleTimeout(5 * time.Second)
		if err != nil {
			errorf(t, "answer had error: %s", err)
			return
		}
		if fiveTuple.Proto != UDP {
			errorf(t, "Protocol was %s instead of udp", fiveTuple.Proto)
			return
		}
		tlog.Debugf("answer got FiveTuple: %s", fiveTuple)
		local, _, err := fiveTuple.UDPAddrs()
		if err != nil {
			errorf(t, "Error in answer: %s", err)
			return
		}
		conn, err := net.ListenUDP("udp", local)
		if err != nil {
			errorf(t, "answer unable to listen on UDP: %s", err)
			return
		}
		tlog.Debugf("Answerer listining on UDP: %s", local)
		answerReady.Done()
		b := make([]byte, 1024)
		for {
			n, addr, err := conn.ReadFrom(b)
			if err != nil {
				errorf(t, "answer unable to read from UDP: %s", err)
				return
			}
			if addr.String() != fiveTuple.Remote {
				errorf(t, "UDP package had address %s, expected %s", addr, fiveTuple.Remote)
				return
			}
			msg := string(b[:n])
			if msg != MESSAGE_TEXT {
				tlog.Debugf("Got message '%s', expected '%s'", msg, MESSAGE_TEXT)
			}
			return
		}
	}()

	// "Signaling" - this would typically be done using a signaling server like
	// waddell when talking to a remote peer

	signal(offer, answer)

	doneCh := make(chan interface{})
	go func() {
		wg.Wait()
		doneCh <- nil
	}()

	select {
	case <-doneCh:
		return
	case <-time.After(1000 * time.Second):
		errorf(t, "Test timed out")
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

func errorf(t *testing.T, msg string, args ...interface{}) {
	tlog.Errorf("error: "+msg, args...)
	t.Errorf(msg, args...)
}
