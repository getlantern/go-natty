// Package natty provides a Go language wrapper to the natty NAT traversal
// utility.  See https://github.com/getlantern/natty.
package natty

import (
	"bufio"
	"encoding/json"
	"io"
	"io/ioutil"
	"os/exec"
	"strings"

	"github.com/oxtoacart/byteexec"
)

const (
	UDP = Protocol("udp")
	TCP = Protocol("tcp")
)

type Protocol string

type FiveTuple struct {
	Proto  Protocol
	Local  string
	Remote string
}

// Natty is a NAT traversal utility.
type Natty struct {
	// OnMessage (required) is called whenever Natty has a message to send to
	// another Natty.  Messages includes things such as SDP and ICE candidates.
	OnMessage func(msg string)
	// DebugOut (optional) is an optional Writer to which debug output from
	// Natty will be written.
	DebugOut  io.Writer
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    io.ReadCloser
	stdoutbuf *bufio.Reader
	stderr    io.ReadCloser
	resultCh  chan *FiveTuple
	errCh     chan error
}

// Offer runs this Natty as an Offerer, meaning that it will make an offer to
// initiate an ICE session.
func (natty *Natty) Offer() (*FiveTuple, error) {
	return natty.run([]string{"-offer"})
}

// Answer runs this Natty as an Answerer, meaning that it will accept offers to
// initiate an ICE session.
func (natty *Natty) Answer() (*FiveTuple, error) {
	return natty.run([]string{})
}

// Message is used to pass this Natty a message from the other Natty.
func (natty *Natty) Message(msg string) error {
	_, err := natty.stdin.Write([]byte(msg))
	if err == nil {
		_, err = natty.stdin.Write([]byte("\n"))
	}
	return err
}

func (natty *Natty) run(params []string) (*FiveTuple, error) {
	err := natty.initCommand(params)
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

	if err != nil {
		return nil, err
	}

	natty.resultCh = make(chan *FiveTuple)
	natty.errCh = make(chan error)
	go natty.processStdout()
	go natty.processStderr()
	go func() {
		natty.errCh <- natty.cmd.Run()
	}()

	for {
		select {
		case result := <-natty.resultCh:
			return result, nil
		case err := <-natty.errCh:
			if err != nil && err != io.EOF {
				return nil, err
			}
		}
	}

	panic("Should never reach here")
}

func (natty *Natty) initCommand(params []string) error {
	if natty.DebugOut == nil {
		// Discard stderr output by default
		natty.DebugOut = ioutil.Discard
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

		if strings.Contains(msg, "5-tuple") {
			fiveTuple := &FiveTuple{}
			err = json.Unmarshal([]byte(msg), fiveTuple)
			if err != nil {
				natty.errCh <- err
				return
			}
			natty.resultCh <- fiveTuple
		} else {
			natty.OnMessage(msg)
		}
	}
}

func (natty *Natty) processStderr() {
	_, err := io.Copy(natty.DebugOut, natty.stderr)
	natty.errCh <- err
}
