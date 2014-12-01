// Package natty provides a Go language wrapper to the natty NAT traversal
// utility.  See https://github.com/getlantern/natty.
//
// See natty_test for an example of Natty in use, including debug logging
// showing the messages that are sent across the signaling channel.
package natty

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/getlantern/byteexec"
	"github.com/getlantern/go-natty/natty/bin"
	"github.com/getlantern/golog"
)

const (
	UDP = Protocol("udp")
	TCP = Protocol("tcp")
)

var (
	log = golog.LoggerFor("natty")

	reallyHighTimeout = 100000 * time.Hour

	nattybe *byteexec.Exec
)

func init() {
	nattyBytes, err := bin.Asset("natty")
	if err != nil {
		panic(fmt.Errorf("Unable to read natty bytes: %s", err))
	}

	nattybe, err = byteexec.New(nattyBytes, "natty")
	if err != nil {
		panic(fmt.Errorf("Unable to construct byteexec for natty: %s", err))
	}
}

type Protocol string

// A FiveTuple is the result of a successful NAT traversal.
type FiveTuple struct {
	Proto  Protocol
	Local  string
	Remote string
}

// UDPAddrs returns a pair of UDPAddrs representing the Local and Remote
// addresses of this FiveTuple. If the FiveTuple's Proto is not UDP, this method
// returns an error.
func (ft *FiveTuple) UDPAddrs() (local *net.UDPAddr, remote *net.UDPAddr, err error) {
	if ft.Proto != UDP {
		err = fmt.Errorf("FiveTuple.Proto was not UDP!: %s", ft.Proto)
		return
	}
	local, err = net.ResolveUDPAddr("udp", ft.Local)
	if err != nil {
		err = fmt.Errorf("Unable to resolve local UDP address %s: %s", ft.Local)
		return
	}
	remote, err = net.ResolveUDPAddr("udp", ft.Remote)
	if err != nil {
		err = fmt.Errorf("Unable to resolve remote UDP address %s: %s", ft.Remote)
	}
	return
}

// Traversal represents a single NAT traversal using natty, whose result is
// available via the methods FiveTuple() and FiveTupleTimeout().
//
// Consumers should make sure to call Close() after finishing with this Natty
// in order to make sure the underlying natty process and associated resources
// are closed.
type Traversal struct {
	traceOut           io.Writer       // target for output from natty's stderr
	cmd                *exec.Cmd       // the natty command
	stdin              io.WriteCloser  // pipe to natty's stdin
	stdout             io.ReadCloser   // pipe from natty's stdout
	stdoutbuf          *bufio.Reader   // buffered stdout
	stderr             io.ReadCloser   // pipe from natty's stderr
	msgInCh            chan string     // channel for messages inbound to this Natty
	msgOutCh           chan string     // channel for messages outbound from this Natty
	peerGotFiveTupleCh chan bool       // channel to signal once we know that our peer received their own FiveTuple
	fiveTupleCh        chan *FiveTuple // intermediary channel for the FiveTuple emitted by the natty command
	errCh              chan error      // intermediary channel for any error encountered while running natty
	fiveTupleOutCh     chan *FiveTuple // channel for FiveTuple output
	errOutCh           chan error      // channel for error output
	fiveTupleOut       *FiveTuple      // the output FiveTuple
	errOut             error           // the output error
	outMutex           sync.Mutex      // mutex for synchronizing access to output variables
}

// Offer starts a Traversal as an Offerer, meaning that it will make an offer to
// initiate an ICE session. Call FiveTuple() or FiveTupleTimeout() to get the
// FiveTuple resulting from Traversal.
func Offer() *Traversal {
	log.Trace("Offering")
	t := &Traversal{
		traceOut: log.TraceOut(),
	}
	t.run([]string{"-offer"})
	return t
}

// Answer starts a Traversal as an Answerer, meaning that it will accept offers
// to initiate an ICE session. Call FiveTuple() or FiveTupleTimeout() to get the
// FiveTuple resulting from Traversal.
func Answer() *Traversal {
	log.Trace("Answering")
	t := &Traversal{
		traceOut: log.TraceOut(),
	}
	t.run([]string{})
	return t
}

// MsgIn is used to pass this Traversal a message from the peer t. This method
// is buffered and will typically not block.
func (t *Traversal) MsgIn(msg string) {
	log.Tracef("Got message: %s", msg)
	t.msgInCh <- msg
}

// NextMsgOut gets the next message to pass to the peer.  If done is true, there
// are no more messages to be read, and the currently returned message should be
// ignored.
func (t *Traversal) NextMsgOut() (msg string, done bool) {
	m, ok := <-t.msgOutCh
	log.Tracef("Returning out message: %s", m)
	return m, !ok
}

// FiveTuple gets the FiveTuple from the Traversal, blocking until such is
// available.
func (t *Traversal) FiveTuple() (*FiveTuple, error) {
	log.Trace("Getting FiveTuple")
	return t.FiveTupleTimeout(reallyHighTimeout)
}

// FiveTupleTimeout gets the FiveTuple from the Traversal, blocking until such
// is available or the timeout is hit.
func (t *Traversal) FiveTupleTimeout(timeout time.Duration) (*FiveTuple, error) {
	log.Trace("Getting FiveTupleTimeout")
	t.outMutex.Lock()
	defer t.outMutex.Unlock()

	if t.fiveTupleOut == nil && t.errOut == nil {
		log.Trace("We don't have a result yet, wait for one")
		select {
		case ft := <-t.fiveTupleOutCh:
			log.Tracef("FiveTuple is: %s", ft)
			t.fiveTupleOut = ft
		case err := <-t.errOutCh:
			log.Tracef("Error is: %s", err)
			t.errOut = err
		case <-time.After(timeout):
			log.Trace("Return an error, but don't store it (lets caller try again)")
			return nil, fmt.Errorf("Timed out waiting for five-tuple")
		}
	}

	log.Tracef("FiveTupleTimeout returns %s: %s", t.fiveTupleOut, t.errOut)
	return t.fiveTupleOut, t.errOut
}

// Close closes this Traversal, terminating any outstanding natty process by
// sending SIGKILL. Close blocks until the natty process has terminated, at
// which point any ports that it bound should be available for use.
func (t *Traversal) Close() error {
	if t.cmd != nil && t.cmd.Process != nil {
		log.Trace("Killing natty process")
		t.cmd.Process.Kill()
		log.Trace("Waiting for natty process to die")
		t.cmd.Wait()
		log.Trace("Killed natty process")
	}
	return nil
}

// run runs the natty command to obtain a FiveTuple. The actual running of
// natty happens on a goroutine so that run itself doesn't block.
func (t *Traversal) run(params []string) {
	t.msgInCh = make(chan string, 100)
	t.msgOutCh = make(chan string, 100)

	// Note - these channels are a buffered in order to prevent deadlocks
	t.peerGotFiveTupleCh = make(chan bool, 10)
	t.fiveTupleCh = make(chan *FiveTuple, 10)
	t.errCh = make(chan error, 10)
	t.fiveTupleOutCh = make(chan *FiveTuple, 10)
	t.errOutCh = make(chan error, 10)

	err := t.initCommand(params)

	go func() {
		if err != nil {
			t.errOutCh <- err
			return
		}

		ft, err := t.doRun(params)
		log.Trace("doRun is finished, inform client of the FiveTuple")
		if err != nil {
			log.Tracef("Returning error: %s", err)
			t.errOutCh <- err
		} else {
			log.Tracef("Returning FiveTuple: %s", ft)
			t.fiveTupleOutCh <- ft
		}
	}()
}

// doRun does the running, including resource cleanup.  doRun blocks until
// Close() has finished, meaning that natty is no longer running and whatever
// port it returned in the FiveTuple can now be used for other things.
func (t *Traversal) doRun(params []string) (*FiveTuple, error) {
	defer t.Close()

	go t.processStdout()
	go t.processStderr()

	// Start the natty command
	t.errCh <- t.cmd.Start()

	// Process incoming messages
	go func() {
		for {
			msg := <-t.msgInCh
			log.Tracef("Got incoming message: %s", msg)

			if IsFiveTuple(msg) {
				log.Trace("Incoming message was a FiveTuple!")
				t.peerGotFiveTupleCh <- true
				continue
			}

			log.Trace("Forward message to natty process")
			_, err := t.stdin.Write([]byte(msg))
			if err == nil {
				_, err = t.stdin.Write([]byte("\n"))
			}
			if err != nil {
				log.Tracef("Unable to forward message to natty process: %s: %s", msg, err)
				t.errCh <- err
			} else {
				log.Tracef("Forwarded message to natty process: %s", msg)
			}
		}
	}()

	for {
		select {
		case result := <-t.fiveTupleCh:
			// Wait for peer to get FiveTuple before returning.  If we didn't do
			// this, our natty instance might stop running before the peer
			// finishes its work to get its own FiveTuple.
			log.Trace("Got our own FiveTuple, waiting for peer to get FiveTuple")
			<-t.peerGotFiveTupleCh
			log.Trace("Peer got FiveTuple!")
			return result, nil
		case err := <-t.errCh:
			if err != nil && err != io.EOF {
				return nil, err
			}
		}
	}
}

// initCommand sets up the natty command
func (t *Traversal) initCommand(params []string) (err error) {
	if log.IsTraceEnabled() {
		log.Trace("Telling natty to log debug output")
		params = append(params, "-debug")
	}

	t.cmd = nattybe.Command(params...)
	t.stdin, err = t.cmd.StdinPipe()
	if err != nil {
		return err
	}
	t.stdout, err = t.cmd.StdoutPipe()
	if err != nil {
		return err
	}
	t.stderr, err = t.cmd.StderrPipe()
	if err != nil {
		return err
	}

	t.stdoutbuf = bufio.NewReader(t.stdout)

	return nil
}

// processStdout reads the output from natty and sends it to the msgOutCh. If
// it finds a FiveTuple, it records that.
func (t *Traversal) processStdout() {
	for {
		// Read next message from natty
		msg, err := t.stdoutbuf.ReadString('\n')
		if err != nil {
			t.errCh <- err
			return
		}

		// Request send of message to peer
		t.msgOutCh <- msg

		if IsFiveTuple(msg) {
			// We got a FiveTuple!
			fiveTuple := &FiveTuple{}
			err = json.Unmarshal([]byte(msg), fiveTuple)
			if err != nil {
				t.errCh <- err
				return
			}
			t.fiveTupleCh <- fiveTuple
		}
	}
}

// processStderr copies the output from natty's stderr to the configured
// traceOut
func (t *Traversal) processStderr() {
	_, err := io.Copy(t.traceOut, t.stderr)
	t.errCh <- err
}

func IsFiveTuple(msg string) bool {
	return strings.Contains(msg, "\"type\":\"5-tuple\"")
}
