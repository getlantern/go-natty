package main

import (
	"flag"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/getlantern/go-natty/natty"
	"github.com/getlantern/go-udtrelay/udtrelay"
	"github.com/getlantern/waddell"
	"github.com/hailiang/gosocks"
)

var (
	server    = flag.String("server", "", "Server id (only used when running as a client)")
	socksPort = flag.Int("socksport", 18000, "Port for SOCKS server, default 18000 (only used when running as a client)")
)

func runClient() {
	if *server == "" {
		log.Printf("Please specify a -server id")
		flag.Usage()
		return
	}
	log.Printf("Starting client, connecting to server %s ...", *server)
	sessionId := uint32(rand.Int31())
	serverId, err := waddell.PeerIdFromString(*server)
	if err != nil {
		log.Fatalf("Unable to parse PeerID for server %s: %s", *server, err)
	}

	nt := natty.NewNatty(
		func(msgOut []byte) {
			wc.SendPieces(serverId, idToBytes(sessionId), msgOut)
		},
		nil)

	go receiveMessagesForNatty(nt, sessionId)

	ft, err := nt.Offer()
	if err != nil {
		log.Fatalf("Unable to offer: %s", err)
	}
	log.Printf("Got five tuple: %s", ft)
	runUDTClient(ft)
}

func receiveMessagesForNatty(nt *natty.Natty, sessionId uint32) {
	b := make([]byte, MAX_MESSAGE_SIZE+waddell.WADDELL_OVERHEAD)
	for {
		wm, err := wc.Receive(b)
		if err != nil {
			log.Fatalf("Unable to read message from waddell: %s", err)
		}
		msg := message(wm.Body)
		if msg.getSessionID() != sessionId {
			log.Printf("Got message for unknown session id %d, skipping", msg.getSessionID())
			continue
		}
		nt.Receive(msg.getData())
	}
}

func runUDTClient(ft *natty.FiveTuple) {
	port, err := strconv.Atoi(strings.Split(ft.Local, ":")[1])
	if err != nil {
		log.Fatalf("Unable to extract local port from address %s: %s", ft.Local, err)
	}
	udtClient := &udtrelay.Client{
		SOCKSPort: *socksPort,
		Port:      port,
		PeerAddr:  ft.Remote,
		// DebugOut:  os.Stderr,
	}
	go func() {
		err = udtClient.Run()
		if err != nil {
			log.Fatalf("Client error: %s", err)
		}
	}()
	defer udtClient.Stop()

	// Wait for client proxy to come up
	time.Sleep(1 * time.Second)

	// Request our geolocation info
	clientPtr := prepareProxyClient(udtClient)
	body, err := httpGetBody(clientPtr, "http://www.google.com/humans.txt")
	if err != nil {
		log.Fatalf("Error requesting geo info: %s", err)
		return
	}
	log.Printf("%s", string(body))
}

func prepareProxyClient(client *udtrelay.Client) *http.Client {
	dialSocksProxy := socks.DialSocksProxy(socks.SOCKS4, "127.0.0.1:18000")
	transport := &http.Transport{Dial: dialSocksProxy}
	return &http.Client{Transport: transport}
}

func httpGet(httpClient *http.Client, url string) (resp *http.Response, err error) {
	req, err := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", "curl/7.21.4 (universal-apple-darwin11.0) libcurl/7.21.4 OpenSSL/0.9.8x zlib/1.2.5")
	resp, err = httpClient.Do(req)
	return
}

func httpGetBody(httpClient *http.Client, url string) (body string, err error) {
	resp, err := httpGet(httpClient, url)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	bodyb, err := ioutil.ReadAll(resp.Body)
	body = string(bodyb)
	return
}
