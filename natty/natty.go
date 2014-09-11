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

type Natty struct {
	OnMessage func(msg string)
	ErrOut    io.Writer
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    io.ReadCloser
	stdoutbuf *bufio.Reader
	stderr    io.ReadCloser
	resultCh  chan *FiveTuple
	errCh     chan error
}

func (natty *Natty) Offer() (*FiveTuple, error) {
	return natty.run([]string{"-offer"})
}

func (natty *Natty) Answer() (*FiveTuple, error) {
	return natty.run([]string{})
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
	if natty.ErrOut == nil {
		// Discard stderr output by default
		natty.ErrOut = ioutil.Discard
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
	// Handle stdout
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
	// Handle stderr
	_, err := io.Copy(natty.ErrOut, natty.stderr)
	natty.errCh <- err
}

func (natty *Natty) Message(msg string) error {
	_, err := natty.stdin.Write([]byte(msg))
	if err == nil {
		_, err = natty.stdin.Write([]byte("\n"))
	}
	return err
}
