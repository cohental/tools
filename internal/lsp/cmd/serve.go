// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmd

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/tools/internal/jsonrpc2"
	"golang.org/x/tools/internal/lsp/cache"
	"golang.org/x/tools/internal/lsp/debug"
	"golang.org/x/tools/internal/lsp/lsprpc"
	"golang.org/x/tools/internal/lsp/protocol"
	"golang.org/x/tools/internal/tool"
	errors "golang.org/x/xerrors"
)

// Serve is a struct that exposes the configurable parts of the LSP server as
// flags, in the right form for tool.Main to consume.
type Serve struct {
	Logfile string `flag:"logfile" help:"filename to log to. if value is \"auto\", then logging to a default output file is enabled"`
	Mode    string `flag:"mode" help:"no effect"`
	Port    int    `flag:"port" help:"port on which to run gopls for debugging purposes"`
	Address string `flag:"listen" help:"address on which to listen for remote connections"`
	Trace   bool   `flag:"rpc.trace" help:"print the full rpc trace in lsp inspector format"`
	Debug   string `flag:"debug" help:"serve debug information on the supplied address"`

	app *Application
}

func (s *Serve) Name() string  { return "serve" }
func (s *Serve) Usage() string { return "" }
func (s *Serve) ShortHelp() string {
	return "run a server for Go code using the Language Server Protocol"
}
func (s *Serve) DetailedHelp(f *flag.FlagSet) {
	fmt.Fprint(f.Output(), `
The server communicates using JSONRPC2 on stdin and stdout, and is intended to be run directly as
a child of an editor process.

gopls server flags are:
`)
	f.PrintDefaults()
}

// Run configures a server based on the flags, and then runs it.
// It blocks until the server shuts down.
func (s *Serve) Run(ctx context.Context, args ...string) error {
	if len(args) > 0 {
		return tool.CommandLineErrorf("server does not take arguments, got %v", args)
	}
	out := os.Stderr
	logfile := s.Logfile
	if logfile != "" {
		if logfile == "auto" {
			logfile = filepath.Join(os.TempDir(), fmt.Sprintf("gopls-%d.log", os.Getpid()))
		}
		f, err := os.Create(logfile)
		if err != nil {
			return errors.Errorf("Unable to create log file: %v", err)
		}
		defer f.Close()
		log.SetOutput(io.MultiWriter(os.Stderr, f))
		out = f
	}

	debug := debug.Instance{
		Logfile:       logfile,
		StartTime:     time.Now(),
		ServerAddress: s.Address,
		DebugAddress:  s.Debug,
		Workdir:       s.app.wd,
	}
	debug.Serve(ctx, s.Debug)

	if s.app.Remote != "" {
		return s.forward()
	}

	ss := lsprpc.NewStreamServer(cache.New(s.app.options), true)
	if s.Address != "" {
		return jsonrpc2.ListenAndServe(ctx, s.Address, ss)
	}
	if s.Port != 0 {
		addr := fmt.Sprintf(":%v", s.Port)
		return jsonrpc2.ListenAndServe(ctx, addr, ss)
	}
	stream := jsonrpc2.NewHeaderStream(os.Stdin, os.Stdout)
	if s.Trace {
		stream = protocol.LoggingStream(stream, out)
	}
	return ss.ServeStream(ctx, stream)
}

func (s *Serve) forward() error {
	conn, err := net.Dial("tcp", s.app.Remote)
	if err != nil {
		return err
	}
	errc := make(chan error)

	go func(conn net.Conn) {
		_, err := io.Copy(conn, os.Stdin)
		errc <- err
	}(conn)

	go func(conn net.Conn) {
		_, err := io.Copy(os.Stdout, conn)
		errc <- err
	}(conn)

	return <-errc
}
