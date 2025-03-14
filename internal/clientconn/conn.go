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

// Package clientconn provides wire protocol server implementation.
package clientconn

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/FerretDB/wire"
	"github.com/pmezard/go-difflib/difflib"
	"go.opentelemetry.io/otel"
	otelattribute "go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/FerretDB/FerretDB/v2/internal/clientconn/conninfo"
	"github.com/FerretDB/FerretDB/v2/internal/clientconn/connmetrics"
	"github.com/FerretDB/FerretDB/v2/internal/handler"
	"github.com/FerretDB/FerretDB/v2/internal/handler/proxy"
	"github.com/FerretDB/FerretDB/v2/internal/mongoerrors"
	"github.com/FerretDB/FerretDB/v2/internal/util/lazyerrors"
	"github.com/FerretDB/FerretDB/v2/internal/util/logging"
	"github.com/FerretDB/FerretDB/v2/internal/util/must"
	"github.com/FerretDB/FerretDB/v2/internal/util/observability"
)

// Mode represents FerretDB mode of operation.
type Mode string

const (
	// NormalMode only handles requests.
	NormalMode Mode = "normal"
	// ProxyMode only proxies requests to another wire protocol compatible service.
	ProxyMode Mode = "proxy"
	// DiffNormalMode both handles requests and proxies them, then logs the diff.
	// Only the FerretDB response is sent to the client.
	DiffNormalMode Mode = "diff-normal"
	// DiffProxyMode both handles requests and proxies them, then logs the diff.
	// Only the proxy response is sent to the client.
	DiffProxyMode Mode = "diff-proxy"
)

// AllModes includes all operation modes, with the first one being the default.
var AllModes = []string{
	string(NormalMode),
	string(ProxyMode),
	string(DiffNormalMode),
	string(DiffProxyMode),
}

// conn represents client connection.
type conn struct {
	netConn        net.Conn
	mode           Mode
	l              *slog.Logger
	h              *handler.Handler
	m              *connmetrics.ConnMetrics
	proxy          *proxy.Router
	lastRequestID  atomic.Int32
	testRecordsDir string // if empty, no records are created
}

// newConnOpts represents newConn options.
type newConnOpts struct {
	netConn     net.Conn
	mode        Mode
	l           *slog.Logger
	handler     *handler.Handler
	connMetrics *connmetrics.ConnMetrics

	proxyAddr        string
	proxyTLSCertFile string
	proxyTLSKeyFile  string
	proxyTLSCAFile   string

	testRecordsDir string // if empty, no records are created
}

// requestFunc represents a function that handles a wire message and returns a wire message response.
type requestFunc func(context.Context, *wire.MsgHeader, wire.MsgBody, string) (*wire.MsgHeader, wire.MsgBody, bool)

// newConn creates a new client connection for given net.Conn.
func newConn(opts *newConnOpts) (*conn, error) {
	if opts.mode == "" {
		panic("mode required")
	}
	if opts.handler == nil {
		panic("handler required")
	}

	var p *proxy.Router
	if opts.mode != NormalMode {
		var err error
		if p, err = proxy.New(opts.proxyAddr, opts.proxyTLSCertFile, opts.proxyTLSKeyFile, opts.proxyTLSCAFile); err != nil {
			return nil, lazyerrors.Error(err)
		}
	}

	return &conn{
		netConn:        opts.netConn,
		mode:           opts.mode,
		l:              opts.l,
		h:              opts.handler,
		m:              opts.connMetrics,
		proxy:          p,
		testRecordsDir: opts.testRecordsDir,
	}, nil
}

// run runs the client connection until ctx is canceled, client disconnects,
// or fatal error or panic is encountered.
//
// Returned error is always non-nil.
//
// The caller is responsible for closing the underlying net.Conn.
func (c *conn) run(ctx context.Context) (err error) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer func() {
		cancel(lazyerrors.Errorf("run exits: %w", err))
	}()

	connInfo := conninfo.New()
	if c.netConn.RemoteAddr().Network() != "unix" {
		connInfo.Peer, err = netip.ParseAddrPort(c.netConn.RemoteAddr().String())
		if err != nil {
			return
		}
	}

	ctx = conninfo.Ctx(ctx, connInfo)

	done := make(chan struct{})

	// handle ctx cancellation
	go func() {
		select {
		case <-done:
			// nothing, let goroutine exit
		case <-ctx.Done():
			// unblocks ReadMessage below; any non-zero past value will do
			if e := c.netConn.SetDeadline(time.Unix(0, 0)); e != nil {
				c.l.WarnContext(ctx, fmt.Sprintf("Failed to set deadline: %s", e))
			}
		}
	}()

	defer func() {
		if p := recover(); p != nil {
			c.l.LogAttrs(ctx, logging.LevelDPanic, fmt.Sprint(p), logging.Error(err))
			err = errors.New("panic")
		}

		// let goroutine above exit
		close(done)
	}()

	bufr := bufio.NewReader(c.netConn)

	// if test record path is set, split netConn reader to write to file and bufr
	if c.testRecordsDir != "" {
		if err = os.MkdirAll(c.testRecordsDir, 0o777); err != nil {
			return
		}

		// write to temporary file first, then rename to avoid partial files

		// use local directory so os.Rename below always works
		var f *os.File
		if f, err = os.CreateTemp(c.testRecordsDir, "_*.partial"); err != nil {
			return
		}

		h := sha256.New()

		defer func() {
			c.renamePartialFile(ctx, f, h, err)
		}()

		r := io.TeeReader(c.netConn, io.MultiWriter(f, h))
		bufr = bufio.NewReader(r)
	}

	bufw := bufio.NewWriter(c.netConn)

	defer func() {
		if e := bufw.Flush(); err == nil {
			err = e
		}

		if c.proxy != nil {
			c.proxy.Close()
		}

		// c.netConn is closed by the caller
	}()

	for {
		if err = c.processMessage(ctx, bufr, bufw); err != nil {
			return
		}
	}
}

// processMessage reads the request, routes the request based on the operation mode
// and writes the response.
//
// Any error returned indicates the connection should be closed.
func (c *conn) processMessage(ctx context.Context, bufr *bufio.Reader, bufw *bufio.Writer) error {
	reqHeader, reqBody, err := wire.ReadMessage(bufr)
	if err != nil {
		return err
	}

	if c.l.Enabled(ctx, slog.LevelDebug) {
		c.l.DebugContext(ctx, "Request header: "+reqHeader.String())
		c.l.DebugContext(ctx, "Request message:\n"+reqBody.StringIndent())
	}

	// diffLogLevel provides the level of logging for the diff between the "normal" and "proxy" responses.
	// It is set to the highest level of logging used to log response.
	diffLogLevel := slog.LevelDebug

	command, comment := extractCommandAndComment(reqHeader, reqBody)

	// TODO https://github.com/FerretDB/FerretDB/issues/1997
	// send requests in diff mode in parallel to align traces
	// by creating a copy of the request body
	//
	// send request to proxy first (unless we are in normal mode)
	// because FerretDB's handling could modify reqBody's documents,
	// creating a data race
	var proxyHeader *wire.MsgHeader
	var proxyBody wire.MsgBody

	if c.mode != NormalMode {
		if c.proxy == nil {
			panic("proxy addr was nil")
		}

		// TODO https://github.com/FerretDB/FerretDB/issues/1997
		proxyHeader, proxyBody = c.proxy.Route(ctx, reqHeader, reqBody)
	}

	// handle request unless we are in proxy mode
	var resCloseConn bool
	var resHeader *wire.MsgHeader
	var resBody wire.MsgBody

	if c.mode != ProxyMode {
		routeWithTrace := c.traceRequest(c.route, comment, c.l)
		resHeader, resBody, resCloseConn = routeWithTrace(ctx, reqHeader, reqBody, command)
		if level := c.logResponse(ctx, "Response", resHeader, resBody, resCloseConn); level > diffLogLevel {
			diffLogLevel = level
		}
	}

	// log proxy response after the normal response to make it less confusing
	if c.mode != NormalMode {
		if level := c.logResponse(ctx, "Proxy response", proxyHeader, proxyBody, false); level > diffLogLevel {
			diffLogLevel = level
		}
	}

	// diff in diff mode
	if c.l.Enabled(ctx, diffLogLevel) && (c.mode == DiffNormalMode || c.mode == DiffProxyMode) {
		if err = c.logDiff(ctx, resHeader, proxyHeader, resBody, proxyBody, diffLogLevel); err != nil {
			return err
		}
	}

	// replace response with one from proxy in proxy and diff-proxy modes
	if c.mode == ProxyMode || c.mode == DiffProxyMode {
		resHeader = proxyHeader
		resBody = proxyBody
	}

	if resHeader == nil || resBody == nil {
		panic("no response to send to client")
	}

	if err = wire.WriteMessage(bufw, resHeader, resBody); err != nil {
		c.l.DebugContext(ctx, "Failed to write message", logging.Error(err))

		return err
	}

	if err = bufw.Flush(); err != nil {
		c.l.DebugContext(ctx, "Failed to flush buffer", logging.Error(err))

		return err
	}

	if resCloseConn {
		err = errors.New("fatal error")

		c.l.DebugContext(ctx, "Connection closed unexpectedly", logging.Error(err))

		return err
	}

	return nil
}

// route sends request to a handler's command based on the op code provided in the request header.
//
// The passed context is canceled when the client disconnects.
//
// Handlers to which it routes, should not panic on bad input, but may do so in "impossible" cases.
// They also should not use recover(). That allows us to use fuzzing.
//
// Returned resBody can be nil.
func (c *conn) route(connCtx context.Context, reqHeader *wire.MsgHeader, reqBody wire.MsgBody, command string) (resHeader *wire.MsgHeader, resBody wire.MsgBody, closeConn bool) { //nolint:lll // argument list is too long
	span := oteltrace.SpanFromContext(connCtx)

	var result, argument string
	defer func() {
		setSpanAttribute(span, result, argument)
		c.m.Responses.WithLabelValues(resHeader.OpCode.String(), command, argument, result).Inc()
	}()

	resHeader = new(wire.MsgHeader)
	var err error
	switch reqHeader.OpCode {
	case wire.OpCodeMsg:
		msg := reqBody.(*wire.OpMsg)

		resHeader.OpCode = wire.OpCodeMsg

		resBody = c.handleOpMsg(connCtx, msg, command)

	case wire.OpCodeQuery:
		query := reqBody.(*wire.OpQuery)
		resHeader.OpCode = wire.OpCodeReply

		// do not store typed nil in interface, it makes it non-nil

		var resReply *wire.OpReply
		resReply, err = c.h.CmdQuery(connCtx, query)

		if resReply != nil {
			resBody = resReply
		}

	case wire.OpCodeReply:
		fallthrough
	case wire.OpCodeUpdate:
		fallthrough
	case wire.OpCodeInsert:
		fallthrough
	case wire.OpCodeGetByOID:
		fallthrough
	case wire.OpCodeGetMore:
		fallthrough
	case wire.OpCodeDelete:
		fallthrough
	case wire.OpCodeKillCursors:
		fallthrough
	case wire.OpCodeCompressed:
		err = lazyerrors.Errorf("unhandled OpCode %s", reqHeader.OpCode)

	default:
		err = lazyerrors.Errorf("unexpected OpCode %s", reqHeader.OpCode)
	}

	if command == "" {
		command = "unknown"
	}

	c.m.Requests.WithLabelValues(reqHeader.OpCode.String(), command).Inc()

	// set body for error
	if err != nil {
		switch resHeader.OpCode {
		case wire.OpCodeMsg:
			protoErr := mongoerrors.Make(connCtx, err, "", c.l)
			resBody = protoErr.Msg()
			result = protoErr.Name
			argument = protoErr.Argument

		case wire.OpCodeReply:
			protoErr := mongoerrors.Make(connCtx, err, "", c.l)
			resBody = protoErr.Reply()
			result = protoErr.Name
			argument = protoErr.Argument

		case wire.OpCodeQuery:
			fallthrough
		case wire.OpCodeUpdate:
			fallthrough
		case wire.OpCodeInsert:
			fallthrough
		case wire.OpCodeGetByOID:
			fallthrough
		case wire.OpCodeGetMore:
			fallthrough
		case wire.OpCodeDelete:
			fallthrough
		case wire.OpCodeKillCursors:
			fallthrough
		case wire.OpCodeCompressed:
			// do not panic to make fuzzing easier
			closeConn = true
			result = "unhandled"

			c.l.ErrorContext(
				connCtx,
				"Handler error for unhandled response opcode",
				logging.Error(err),
				slog.Any("opcode", resHeader.OpCode),
			)
			return

		default:
			// do not panic to make fuzzing easier
			closeConn = true
			result = "unexpected"

			c.l.ErrorContext(
				connCtx,
				"Handler error for unexpected response opcode",
				logging.Error(err),
				slog.Any("opcode", resHeader.OpCode),
			)
			return
		}
	}

	// Don't call MarshalBinary there. Fix header in the caller?
	// TODO https://github.com/FerretDB/FerretDB/issues/273
	b, err := resBody.MarshalBinary()
	if err != nil {
		result = ""
		panic(err)
	}
	resHeader.MessageLength = int32(wire.MsgHeaderLen + len(b))

	resHeader.RequestID = c.lastRequestID.Add(1)
	resHeader.ResponseTo = reqHeader.RequestID

	if result == "" {
		result = "ok"
	}

	return
}

// traceRequest wraps the function `f` with OpenTelemetry tracer.
func (c *conn) traceRequest(f requestFunc, comment string, l *slog.Logger) requestFunc {
	return func(ctx context.Context, header *wire.MsgHeader, body wire.MsgBody, command string) (*wire.MsgHeader, wire.MsgBody, bool) { //nolint:lll // for readability
		ctx, span := startSpan(ctx, comment, l)

		defer endSpan(span, command, header.OpCode.String(), int(header.ResponseTo))

		return f(ctx, header, body, command)
	}
}

// collectMetrics wraps the function `f` with metrics collector.
func (c *conn) collectMetrics(f requestFunc) requestFunc {
	return func(ctx context.Context, header *wire.MsgHeader, body wire.MsgBody, command string) (*wire.MsgHeader, wire.MsgBody, bool) { //nolint:lll // for readability
		c.m.Requests.WithLabelValues(header.OpCode.String(), command).Inc()

		defer c.m.Responses.WithLabelValues(header.OpCode.String(), command).Inc()

		return f(ctx, header, body, command)
	}
}

// extractCommandAndComment extracts the command and comment by decoding OpMsg body.
// If the request body is not an OpMsg, it returns empty strings.
func extractCommandAndComment(reqHeader *wire.MsgHeader, reqBody wire.MsgBody) (string, string) {
	switch reqHeader.OpCode {
	case wire.OpCodeMsg:
		msg := reqBody.(*wire.OpMsg)
		raw := msg.RawSection0()

		doc, err := raw.Decode()
		if err != nil {
			return "", ""
		}

		command := doc.Command()
		comment, _ := doc.Get("comment").(string)

		return command, comment
	case wire.OpCodeQuery:
		fallthrough
	case wire.OpCodeReply:
		fallthrough
	case wire.OpCodeUpdate:
		fallthrough
	case wire.OpCodeInsert:
		fallthrough
	case wire.OpCodeGetByOID:
		fallthrough
	case wire.OpCodeGetMore:
		fallthrough
	case wire.OpCodeDelete:
		fallthrough
	case wire.OpCodeKillCursors:
		fallthrough
	case wire.OpCodeCompressed:
		fallthrough
	default:
		return "", ""
	}
}

// startSpan gets the parent span from the comment field of the document,
// and starts a new span with it.
// If there is no span context, a new span without parent is started.
func startSpan(ctx context.Context, comment string, l *slog.Logger) (context.Context, oteltrace.Span) {
	var span oteltrace.Span

	spanCtx, err := observability.SpanContextFromComment(comment)
	if err != nil {
		l.DebugContext(ctx, "Failed to extract span context from comment", logging.Error(err))
		ctx, span = otel.Tracer("").Start(ctx, "")

		return ctx, span
	}

	ctx = oteltrace.ContextWithRemoteSpanContext(ctx, spanCtx)
	ctx, span = otel.Tracer("").Start(ctx, "")

	return ctx, span
}

// setSpanAttribute sets the span status and argument attribute.
func setSpanAttribute(span oteltrace.Span, result, argument string) {
	must.NotBeZero(span)

	if result == "" {
		result = "panic"
	}

	if argument == "" {
		argument = "unknown"
	}

	if result != "ok" {
		span.SetStatus(otelcodes.Error, result)
	}

	span.SetAttributes(otelattribute.String("db.ferretdb.argument", argument))
}

// endSpan ends the span by setting name and attributes to the span.
func endSpan(span oteltrace.Span, command, opCode string, responseTo int) {
	must.NotBeZero(span)

	span.SetName(command)
	span.SetAttributes(
		otelattribute.String("db.ferretdb.opcode", opCode),
		otelattribute.Int("db.ferretdb.request_id", responseTo),
	)
	span.End()
}

// handleOpMsg processes OP_MSG requests.
//
// The passed context is canceled when the client disconnects.
func (c *conn) handleOpMsg(connCtx context.Context, msg *wire.OpMsg, command string) *wire.OpMsg {
	cmd, ok := c.h.Commands()[command]
	if !ok || cmd.Handler == nil {
		err := mongoerrors.New(
			mongoerrors.ErrCommandNotFound,
			fmt.Sprintf("no such command: '%s'", command),
		)

		return mongoerrors.Make(connCtx, err, "", c.l).Msg()
	}

	res, err := cmd.Handler(connCtx, msg)
	if err != nil {
		return mongoerrors.Make(connCtx, err, "", c.l).Msg()
	}

	return res
}

// renamePartialFile takes over an open file `f` and closes it.
// It uses the given error to check if the connection was closed by the client,
// if so the given file is renamed to a name generated by hash,
// otherwise, it deletes the given file.
func (c *conn) renamePartialFile(ctx context.Context, f *os.File, h hash.Hash, err error) {
	// do not store partial files
	if !errors.Is(err, wire.ErrZeroRead) {
		_ = f.Close()
		_ = os.Remove(f.Name())

		return
	}

	// surprisingly, Sync is required before Rename on many OS/FS combinations
	if e := f.Sync(); e != nil {
		c.l.WarnContext(ctx, "Failed to sync file", logging.Error(e))
	}

	if e := f.Close(); e != nil {
		c.l.WarnContext(ctx, "Failed to close file", logging.Error(e))
	}

	fileName := hex.EncodeToString(h.Sum(nil))

	hashPath := filepath.Join(c.testRecordsDir, fileName[:2])
	if e := os.MkdirAll(hashPath, 0o777); e != nil {
		c.l.WarnContext(ctx, "Failed to make directory", logging.Error(e))
	}

	path := filepath.Join(hashPath, fileName+".bin")
	if e := os.Rename(f.Name(), path); e != nil {
		c.l.WarnContext(ctx, "Failed to rename file", logging.Error(e))
	}
}

// logResponse logs response's header and body and returns the log level that was used.
//
// The param `who` will be used in logs and should represent the type of the response,
// for example "Response" or "Proxy Response".
func (c *conn) logResponse(ctx context.Context, who string, resHeader *wire.MsgHeader, resBody wire.MsgBody, closeConn bool) slog.Level { //nolint:lll // for readability
	level := slog.LevelDebug

	if resHeader.OpCode == wire.OpCodeMsg {
		msg := resBody.(*wire.OpMsg)

		raw := msg.RawSection0()
		doc, _ := raw.Decode()

		var ok bool

		if doc != nil {
			switch v := doc.Get("ok").(type) {
			case float64:
				ok = v == 1
			case int32:
				ok = v == 1
			case int64:
				ok = v == 1
			}
		}

		if !ok {
			level = slog.LevelWarn
		}
	}

	if closeConn {
		level = slog.LevelError
	}

	if c.l.Enabled(ctx, level) {
		c.l.Log(ctx, level, who+" header: "+resHeader.String())
		c.l.Log(ctx, level, who+" message:\n"+resBody.StringIndent())
	}

	return level
}

// logDiff logs the diff between the response and the proxy response.
func (c *conn) logDiff(ctx context.Context, resHeader, proxyHeader *wire.MsgHeader, resBody, proxyBody wire.MsgBody, logLevel slog.Level) error { //nolint:lll // for readability
	diffHeader, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(resHeader.String()),
		FromFile: "res header",
		B:        difflib.SplitLines(proxyHeader.String()),
		ToFile:   "proxy header",
		Context:  1,
	})
	if err != nil {
		return err
	}

	// resBody can be nil if we got a message we could not handle at all, like unsupported OpQuery.
	var resBodyString, proxyBodyString string

	if resBody != nil {
		resBodyString = resBody.StringIndent()
	}

	if proxyBody != nil {
		proxyBodyString = proxyBody.StringIndent()
	}

	diffBody, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(resBodyString),
		FromFile: "res body",
		B:        difflib.SplitLines(proxyBodyString),
		ToFile:   "proxy body",
		Context:  1,
	})
	if err != nil {
		return err
	}

	if len(diffBody) > 0 {
		// the diff control lines (those with ---, +++, or @@) are created with a trailing newline
		diffBody = "\n" + strings.TrimSpace(diffBody)
	}

	c.l.Log(ctx, logLevel, "Header diff:\n"+diffHeader+"\nBody diff:"+diffBody)

	return nil
}
