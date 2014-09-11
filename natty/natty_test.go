package natty

import (
	"log"
	//"net"
	"sync"
	"testing"
)

func TestNatty(t *testing.T) {
	var offerer *Natty
	var answerer *Natty

	offerer = &Natty{
		OnMessage: func(msg string) {
			answerer.Message(msg)
		},
	}

	answerer = &Natty{
		OnMessage: func(msg string) {
			offerer.Message(msg)
		},
	}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		fiveTuple, err := offerer.Offer()
		if err != nil {
			t.Fatalf("Offerer had error: %s", err)
		}
		log.Printf("Offerer got 5 tuple: %s", fiveTuple)
		if fiveTuple.Proto != UDP {
			t.Fatalf("Protocol was %s instead of udp", fiveTuple.Proto)
		}
		wg.Done()
	}()

	go func() {
		fiveTuple, err := answerer.Answer()
		if err != nil {
			t.Errorf("Answerer had error: %s", err)
		}
		if fiveTuple.Proto != UDP {
			t.Fatalf("Protocol was %s instead of udp", fiveTuple.Proto)
		}
		log.Printf("Answerer got 5 tuple: %s", fiveTuple)
		wg.Done()
	}()

	wg.Wait()
}
