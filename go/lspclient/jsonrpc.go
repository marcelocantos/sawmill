// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package lspclient

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// jsonrpcRequest is a JSON-RPC 2.0 request.
type jsonrpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      *int64 `json:"id,omitempty"` // nil for notifications
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// jsonrpcResponse is a JSON-RPC 2.0 response.
type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *jsonrpcError) Error() string {
	return fmt.Sprintf("JSON-RPC error %d: %s", e.Code, e.Message)
}

// conn manages a JSON-RPC 2.0 connection over an io.ReadWriteCloser using
// LSP's Content-Length framing (HTTP-style headers).
type conn struct {
	rwc     io.ReadWriteCloser
	reader  *bufio.Reader
	writeMu sync.Mutex
	nextID  atomic.Int64

	pendingMu sync.Mutex
	pending   map[int64]chan *jsonrpcResponse

	// notifications receives server-initiated notifications.
	notifications chan *jsonrpcRequest

	done chan struct{}
}

func newConn(rwc io.ReadWriteCloser) *conn {
	c := &conn{
		rwc:           rwc,
		reader:        bufio.NewReader(rwc),
		pending:       make(map[int64]chan *jsonrpcResponse),
		notifications: make(chan *jsonrpcRequest, 64),
		done:          make(chan struct{}),
	}
	go c.readLoop()
	return c
}

// call sends a request and waits for a response.
func (c *conn) call(method string, params, result any) error {
	id := c.nextID.Add(1)
	req := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  method,
		Params:  params,
	}

	ch := make(chan *jsonrpcResponse, 1)
	c.pendingMu.Lock()
	c.pending[id] = ch
	c.pendingMu.Unlock()

	if err := c.write(req); err != nil {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return fmt.Errorf("writing %s request: %w", method, err)
	}

	select {
	case resp := <-ch:
		if resp.Error != nil {
			return resp.Error
		}
		if result != nil && len(resp.Result) > 0 {
			return json.Unmarshal(resp.Result, result)
		}
		return nil
	case <-c.done:
		return fmt.Errorf("connection closed while waiting for %s response", method)
	}
}

// notify sends a notification (no response expected).
func (c *conn) notify(method string, params any) error {
	req := jsonrpcRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	return c.write(req)
}

func (c *conn) write(msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	if _, err := io.WriteString(c.rwc, header); err != nil {
		return err
	}
	_, err = c.rwc.Write(data)
	return err
}

func (c *conn) readLoop() {
	defer close(c.done)

	for {
		length, err := c.readHeaders()
		if err != nil {
			return
		}

		body := make([]byte, length)
		if _, err := io.ReadFull(c.reader, body); err != nil {
			return
		}

		// Try as response first (has "id" and "result"/"error").
		var resp jsonrpcResponse
		if err := json.Unmarshal(body, &resp); err == nil && resp.ID != nil {
			c.pendingMu.Lock()
			ch, ok := c.pending[*resp.ID]
			if ok {
				delete(c.pending, *resp.ID)
			}
			c.pendingMu.Unlock()
			if ok {
				ch <- &resp
			}
			continue
		}

		// Try as notification/request (has "method").
		var req jsonrpcRequest
		if err := json.Unmarshal(body, &req); err == nil && req.Method != "" {
			select {
			case c.notifications <- &req:
			default:
				// Drop if notification buffer is full.
			}
		}
	}
}

func (c *conn) readHeaders() (int, error) {
	var contentLength int
	for {
		line, err := c.reader.ReadString('\n')
		if err != nil {
			return 0, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if strings.HasPrefix(line, "Content-Length:") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "Content-Length:"))
			n, err := strconv.Atoi(val)
			if err != nil {
				return 0, fmt.Errorf("invalid Content-Length %q: %w", val, err)
			}
			contentLength = n
		}
	}
	if contentLength == 0 {
		return 0, fmt.Errorf("missing Content-Length header")
	}
	return contentLength, nil
}

func (c *conn) close() error {
	return c.rwc.Close()
}
