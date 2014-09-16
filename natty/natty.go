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

// Natty is a NAT traversal utility.
type Natty struct {
	params          []string
	send            func(msg string)
	debugOut        io.Writer
	cmd             *exec.Cmd
	stdin           io.WriteCloser
	stdout          io.ReadCloser
	stdoutbuf       *bufio.Reader
	stderr          io.ReadCloser
	msgInCh         chan string
	msgOutCh        chan string
	peerGot5TupleCh chan bool
	resultCh        chan *FiveTuple
	errCh           chan error
	nextFiveTupleCh chan *FiveTuple
	nextErrCh       chan error
}

// Offer runs a Natty as an Offerer, meaning that it will make an offer to
// initiate an ICE session. Call FiveTuple() to get the FiveTuple resulting
// from NAT traversal.
//
// send (required) is called whenever Natty has a message to send to the other
// Natty.  Messages includes things such as SDP and ICE candidates.
//
// debugOut (optional) is an optional Writer to which debug output from Natty
// will be written.
//
func Offer(send func(msg string), debugOut io.Writer) *Natty {
	natty := &Natty{
		send:     send,
		debugOut: debugOut,
	}
	natty.run([]string{"-offer"})
	return natty
}

// Answer runs a Natty as an Answerer, meaning that it will accept offers to
// initiate an ICE session. Call FiveTuple() to get the FiveTuple resulting
// from NAT traversal.
//
// send (required) is called whenever Natty has a message to send to the other
// Natty.  Messages includes things such as SDP and ICE candidates.
//
// debugOut (optional) is an optional Writer to which debug output from Natty
// will be written.
//
func Answer(send func(msg string), debugOut io.Writer) *Natty {
	natty := &Natty{
		send:     send,
		debugOut: debugOut,
	}
	natty.run([]string{})
	return natty
}

// Receive is used to pass this Natty a message from the other Natty. This
// method is buffered and will typically not block.
func (natty *Natty) Receive(msg string) {
	natty.msgInCh <- msg
}

// FiveTuple gets the FiveTuple from the NAT traversal, blocking until such is
// available.  FiveTuple can only be called once!
func (natty *Natty) FiveTuple() (*FiveTuple, error) {
	return natty.FiveTupleTimeout(reallyHighTimeout)
}

// FiveTupleTimeout gets the FiveTuple from the NAT traversal, blocking until
// such is available or the timeout is hit.  FiveTupleTimeout can only be called
// once!
func (natty *Natty) FiveTupleTimeout(timeout time.Duration) (*FiveTuple, error) {
	select {
	case ft := <-natty.nextFiveTupleCh:
		return ft, nil
	case err := <-natty.nextErrCh:
		return nil, err
	case <-time.After(timeout):
		return nil, fmt.Errorf("Timed out waiting for five-tuple")
	}
}

// run runs the Natty command to obtain a FiveTuple. The actual running of
// Natty happens on a goroutine so that run itself doesn't block.
func (natty *Natty) run(params []string) {
	natty.msgInCh = make(chan string, 100)
	natty.msgOutCh = make(chan string, 100)
	natty.peerGot5TupleCh = make(chan bool)
	natty.resultCh = make(chan *FiveTuple, 10)
	natty.errCh = make(chan error, 10)
	natty.nextFiveTupleCh = make(chan *FiveTuple, 10)
	natty.nextErrCh = make(chan error, 10)

	err := natty.initCommand(params)

	go func() {
		if err != nil {
			natty.nextErrCh <- err
			return
		}

		ft, err := natty.doRun(params)
		// Once doRun is finished, inform client of the FiveTuple
		if err != nil {
			natty.nextErrCh <- err
		} else {
			natty.nextFiveTupleCh <- ft
		}
	}()
}

// doRun does the running, including resource cleanup.  Once doRun returns, we
// know that natty is no longer running and whatever port it returned in the
// FiveTuple can now be used for other things.
func (natty *Natty) doRun(params []string) (*FiveTuple, error) {
	defer func() {
		if natty.stdin != nil {
			natty.stdin.Close()
		}
		if natty.stdout != nil {
			natty.stdout.Close()
		}
		if natty.stderr != nil {
			natty.stderr.Close()
		}
	}()

	go natty.processStdout()
	go natty.processStderr()

	// Run the natty command
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		natty.errCh <- natty.cmd.Run()
		wg.Done()
	}()

	// Process incoming messages
	go func() {
		for {
			msg := <-natty.msgInCh
			if is5Tuple(msg) {
				natty.peerGot5TupleCh <- true
				continue
			}
			_, err := natty.stdin.Write([]byte(msg))
			if err == nil {
				_, err = natty.stdin.Write([]byte("\n"))
			}
			if err != nil {
				natty.errCh <- err
			}
		}
	}()

	// Process outgoing messages
	go func() {
		for {
			msg := <-natty.msgOutCh
			natty.send(msg)
		}
	}()

	// Wait for natty process to finish before returning from this function
	defer wg.Wait()

	for {
		select {
		case result := <-natty.resultCh:
			// Wait for peer to get 5 tuple before returning
			<-natty.peerGot5TupleCh
			return result, nil
		case err := <-natty.errCh:
			if err != nil && err != io.EOF {
				return nil, err
			}
		}
	}
}

func (natty *Natty) initCommand(params []string) error {
	if natty.debugOut == nil {
		// Discard stderr output by default
		natty.debugOut = ioutil.Discard
	} else {
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

func (natty *Natty) processStdout() {
	for {
		msg, err := natty.stdoutbuf.ReadString('\n')
		if err != nil {
			natty.errCh <- err
			return
		}

		natty.msgOutCh <- msg
		if is5Tuple(msg) {
			fiveTuple := &FiveTuple{}
			err = json.Unmarshal([]byte(msg), fiveTuple)
			if err != nil {
				natty.errCh <- err
				return
			}
			natty.resultCh <- fiveTuple
		}
	}
}

func (natty *Natty) processStderr() {
	_, err := io.Copy(natty.debugOut, natty.stderr)
	natty.errCh <- err
}

func is5Tuple(msg string) bool {
	return strings.Contains(msg, "\"type\":\"5-tuple\"")
}
