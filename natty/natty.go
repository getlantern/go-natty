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
	"io/ioutil"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/oxtoacart/byteexec"
)

const (
	UDP = Protocol("udp")
	TCP = Protocol("tcp")
)

var (
	reallyHighTimeout = 100000 * time.Hour
)

type Protocol string

// A FiveTuple is the result of a successful NAT traversal.
type FiveTuple struct {
	Proto  Protocol
	Local  string
	Remote string
}

// Natty is a NAT traversal utility. It does a single NAT-traversal and makes
// the result available via the methods FiveTuple() and FiveTupleTimeout().
// Consumer should make sure to call Close() after finishing with this Natty
// in order to make sure the underlying natty process and associated resources
// are closed.
type Natty struct {
	debugOut           io.Writer       // target for output from natty's stderr
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

// Offer runs a Natty as an Offerer, meaning that it will make an offer to
// initiate an ICE session. Call FiveTuple() or FiveTupleTimeout() to get the
// FiveTuple resulting from NAT traversal.
//
// debugOut (optional) is an optional io.Writer to which debug output from Natty
// will be written.
//
func Offer(debugOut io.Writer) *Natty {
	natty := &Natty{
		debugOut: debugOut,
	}
	natty.run([]string{"-offer"})
	return natty
}

// Answer runs a Natty as an Answerer, meaning that it will accept offers to
// initiate an ICE session. Call FiveTuple() or FiveTupleTimeout() to get the
// FiveTuple resulting from NAT traversal.
//
// debugOut (optional) is an optional io.Writer to which debug output from Natty
// will be written.
//
func Answer(debugOut io.Writer) *Natty {
	natty := &Natty{
		debugOut: debugOut,
	}
	natty.run([]string{})
	return natty
}

// MsgIn is used to pass this Natty a message from the peer Natty. This method
// is buffered and will typically not block.
func (natty *Natty) MsgIn(msg string) {
	natty.msgInCh <- msg
}

// NextMsgOut gets the next message to pass to the peer Natty.  If done is true,
// there are no more messages to be read, and the currently returned message
// should be ignored.
func (natty *Natty) NextMsgOut() (msg string, done bool) {
	m, ok := <-natty.msgOutCh
	return m, !ok
}

// FiveTuple gets the FiveTuple from the NAT traversal, blocking until such is
// available.
func (natty *Natty) FiveTuple() (*FiveTuple, error) {
	return natty.FiveTupleTimeout(reallyHighTimeout)
}

// FiveTupleTimeout gets the FiveTuple from the NAT traversal, blocking until
// such is available or the timeout is hit.
func (natty *Natty) FiveTupleTimeout(timeout time.Duration) (*FiveTuple, error) {
	natty.outMutex.Lock()
	defer natty.outMutex.Unlock()

	if natty.fiveTupleOut == nil && natty.errOut == nil {
		// We don't have a result yet, wait for one
		select {
		case ft := <-natty.fiveTupleOutCh:
			natty.fiveTupleOut = ft
		case err := <-natty.errOutCh:
			natty.errOut = err
		case <-time.After(timeout):
			// Return an error, but don't store it (lets caller try again)
			return nil, fmt.Errorf("Timed out waiting for five-tuple")
		}
	}

	return natty.fiveTupleOut, natty.errOut
}

// Close closes this Natty, terminating any outstanding Natty process by sending
// SIGKILL. Close blocks until the natty process has terminated, at which point
// any ports that it bound should be available for use.
func (natty *Natty) Close() error {
	natty.closePipes()

	if natty.cmd != nil && natty.cmd.Process != nil {
		natty.cmd.Process.Kill()
		natty.cmd.Process.Wait()
	}
	return nil
}

// run runs the Natty command to obtain a FiveTuple. The actual running of
// Natty happens on a goroutine so that run itself doesn't block.
func (natty *Natty) run(params []string) {
	natty.msgInCh = make(chan string, 100)
	natty.msgOutCh = make(chan string, 100)

	// Note - these channels are a little oversized to prevent deadlocks
	natty.peerGotFiveTupleCh = make(chan bool, 10)
	natty.fiveTupleCh = make(chan *FiveTuple, 10)
	natty.errCh = make(chan error, 10)
	natty.fiveTupleOutCh = make(chan *FiveTuple, 10)
	natty.errOutCh = make(chan error, 10)

	err := natty.initCommand(params)

	go func() {
		if err != nil {
			natty.errOutCh <- err
			return
		}

		ft, err := natty.doRun(params)
		// Once doRun is finished, inform client of the FiveTuple
		if err != nil {
			natty.errOutCh <- err
		} else {
			natty.fiveTupleOutCh <- ft
		}
	}()
}

// doRun does the running, including resource cleanup.  doRun blocks until
// Close() has finished, meaning that natty is no longer running and whatever
// port it returned in the FiveTuple can now be used for other things.
func (natty *Natty) doRun(params []string) (*FiveTuple, error) {
	defer natty.Close()

	go natty.processStdout()
	go natty.processStderr()

	// Start the natty command
	natty.errCh <- natty.cmd.Start()

	// Process incoming messages
	go func() {
		for {
			msg := <-natty.msgInCh

			if isFiveTuple(msg) {
				natty.peerGotFiveTupleCh <- true
				continue
			}

			// Forward message to natty process
			_, err := natty.stdin.Write([]byte(msg))
			if err == nil {
				_, err = natty.stdin.Write([]byte("\n"))
			}
			if err != nil {
				natty.errCh <- err
			}
		}
	}()

	for {
		select {
		case result := <-natty.fiveTupleCh:
			// Wait for peer to get FiveTuple before returning.  If we didn't do
			// this, our natty instance might stop running before the peer
			// finishes its work to get its own FiveTuple.
			<-natty.peerGotFiveTupleCh
			return result, nil
		case err := <-natty.errCh:
			if err != nil && err != io.EOF {
				return nil, err
			}
		}
	}
}

// initCommand sets up the natty command
func (natty *Natty) initCommand(params []string) error {
	if natty.debugOut == nil {
		// Discard stderr output by default
		natty.debugOut = ioutil.Discard
	} else {
		// Tell natty to log debug output
		params = append(params, "-debug")
	}

	nattyBytes, err := Asset("natty")
	if err != nil {
		return err
	}
	be, err := byteexec.NewByteExec(nattyBytes)
	if err != nil {
		return err
	}

	natty.cmd = be.Command(params...)
	natty.stdin, err = natty.cmd.StdinPipe()
	if err != nil {
		return err
	}
	natty.stdout, err = natty.cmd.StdoutPipe()
	if err != nil {
		return err
	}
	natty.stderr, err = natty.cmd.StderrPipe()
	if err != nil {
		return err
	}

	natty.stdoutbuf = bufio.NewReader(natty.stdout)

	return nil
}

// processStdout reads the output from natty and sends it to the msgOutCh. If
// it finds a FiveTuple, it records that.
func (natty *Natty) processStdout() {
	for {
		// Read next message from natty
		msg, err := natty.stdoutbuf.ReadString('\n')
		if err != nil {
			natty.errCh <- err
			return
		}

		// Request send of message to peer
		natty.msgOutCh <- msg

		if isFiveTuple(msg) {
			// We got a FiveTuple!
			fiveTuple := &FiveTuple{}
			err = json.Unmarshal([]byte(msg), fiveTuple)
			if err != nil {
				natty.errCh <- err
				return
			}
			natty.fiveTupleCh <- fiveTuple
		}
	}
}

// processStderr copies the output from natty's stderr to the configured
// DebugOut
func (natty *Natty) processStderr() {
	_, err := io.Copy(natty.debugOut, natty.stderr)
	natty.errCh <- err
}

func (natty *Natty) closePipes() {
	if natty.stdin != nil {
		natty.stdin.Close()
	}
	if natty.stdout != nil {
		natty.stdout.Close()
	}
	if natty.stderr != nil {
		natty.stderr.Close()
	}
}

func isFiveTuple(msg string) bool {
	return strings.Contains(msg, "\"type\":\"5-tuple\"")
}
