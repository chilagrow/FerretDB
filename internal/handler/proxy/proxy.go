// Copyright 2021 FerretDB Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package proxy handles messages by sending them to another wire protocol compatible service.
package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"net"

	"github.com/FerretDB/wire"

	"github.com/FerretDB/FerretDB/v2/internal/util/lazyerrors"
	"github.com/FerretDB/FerretDB/v2/internal/util/tlsutil"
)

// Handler handles messages by sending them to another wire protocol compatible service.
type Handler struct {
	conn net.Conn
	bufr *bufio.Reader
	bufw *bufio.Writer
}

// New creates a new Handler for a service with given address.
func New(addr, certFile, keyFile, caFile string) (*Handler, error) {
	var conn net.Conn
	var err error

	if certFile != "" {
		conn, err = dialTLS(addr, certFile, keyFile, caFile)
	} else {
		conn, err = net.Dial("tcp", addr)
	}

	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	return &Handler{
		conn: conn,
		bufr: bufio.NewReader(conn),
		bufw: bufio.NewWriter(conn),
	}, nil
}

// dialTLS connects to the given address using TLS.
func dialTLS(addr, certFile, keyFile, caFile string) (net.Conn, error) {
	config, err := tlsutil.Config(certFile, keyFile, caFile)
	if err != nil {
		return nil, err
	}

	conn, err := tls.Dial("tcp", addr, config)
	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	if err = conn.Handshake(); err != nil {
		return nil, lazyerrors.Error(err)
	}

	return conn, nil
}

// Close stops the handler.
func (r *Handler) Close() {
	r.conn.Close()
}

// Handle handles the message by sending it to another wire protocol compatible service.
func (r *Handler) Handle(ctx context.Context, header *wire.MsgHeader, body wire.MsgBody) (*wire.MsgHeader, wire.MsgBody) {
	deadline, _ := ctx.Deadline()
	r.conn.SetDeadline(deadline)

	if err := wire.WriteMessage(r.bufw, header, body); err != nil {
		panic(err)
	}

	if err := r.bufw.Flush(); err != nil {
		panic(err)
	}

	resHeader, resBody, err := wire.ReadMessage(r.bufr)
	if err != nil {
		panic(err)
	}

	return resHeader, resBody
}
