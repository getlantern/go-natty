package main

import (
	"encoding/binary"
	"flag"
	"log"
	"net"
	"time"

	"github.com/getlantern/waddell"
)

const (
	// TODO: figure out maximum required size for messages
	MAX_MESSAGE_SIZE = 4096

	READY = "READY"

	TIMEOUT = 15 * time.Second
)

var (
	endianness = binary.LittleEndian

	help        = flag.Bool("help", false, "Get usage help")
	mode        = flag.String("mode", "client", "client or server. Client initiates the NAT traversal. Defaults to client.")
	waddellAddr = flag.String("waddell", "128.199.130.61:443", "Address of waddell signaling server, defaults to 128.199.130.61:443")

	wc *waddell.Client
)

// message represents a message exchanged during a NAT traversal
type message []byte

func (msg message) setTraversalId(id uint32) {
	endianness.PutUint32(msg[:4], id)
}

func (msg message) getTraversalId() uint32 {
	return endianness.Uint32(msg[:4])
}

func (msg message) getData() []byte {
	return msg[4:]
}

func idToBytes(id uint32) []byte {
	b := make([]byte, 4)
	endianness.PutUint32(b[:4], id)
	return b
}

func main() {
	flag.Parse()
	if *help {
		flag.Usage()
		return
	}

	connectToWaddell()

	if "server" == *mode {
		runServer()
	} else {
		runClient()
	}
}

func connectToWaddell() {
	conn, err := net.Dial("tcp", *waddellAddr)
	if err != nil {
		log.Fatalf("Unable to dial waddell: %s", err)
	}
	wc, err = waddell.Connect(conn)
	if err != nil {
		log.Fatalf("Unable to connect to waddell: %s", err)
	}
}
