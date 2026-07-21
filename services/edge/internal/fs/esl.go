package fs

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/fiorix/go-eventsocket/eventsocket"
)

// APIClient is the FreeSWITCH ESL surface used by gateway apply.
type APIClient interface {
	API(cmd string) (string, error)
}

// Client is a reconnecting inbound ESL client.
type Client struct {
	addr     string
	password string

	mu   sync.Mutex
	conn *eventsocket.Connection
}

// NewClient returns an ESL client for addr (host:port).
func NewClient(addr, password string) *Client {
	return &Client{addr: addr, password: password}
}

// ConnectOnce dials and authenticates once.
func (c *Client) ConnectOnce() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connectLocked()
}

func (c *Client) connectLocked() error {
	conn, err := eventsocket.Dial(c.addr, c.password)
	if err != nil {
		return err
	}
	if c.conn != nil {
		c.conn.Close()
	}
	c.conn = conn
	return nil
}

// RunReconnect keeps an ESL session alive until ctx is cancelled.
func (c *Client) RunReconnect(ctx context.Context) {
	backoff := time.Second
	const maxBackoff = 30 * time.Second
	for {
		if ctx.Err() != nil {
			c.Close()
			return
		}
		if err := c.ConnectOnce(); err != nil {
			log.Printf("fs esl: connect %s: %v (retry in %s)", c.addr, err, backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}
		log.Printf("fs esl: connected to %s", c.addr)
		backoff = time.Second
		// Block until disconnect by issuing a lightweight keepalive via API.
		for {
			if ctx.Err() != nil {
				c.Close()
				return
			}
			if _, err := c.API("status"); err != nil {
				log.Printf("fs esl: connection lost: %v", err)
				break
			}
			select {
			case <-ctx.Done():
				c.Close()
				return
			case <-time.After(15 * time.Second):
			}
		}
	}
}

// API runs an ESL api command and returns the response body.
// The mutex is held for the full request/response so concurrent callers
// (e.g. status keepalive vs ReloadAll) cannot interleave on one connection.
func (c *Client) API(cmd string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		if err := c.connectLocked(); err != nil {
			return "", err
		}
	}
	ev, err := c.conn.Send("api " + cmd)
	if err != nil {
		c.conn.Close()
		c.conn = nil
		return "", err
	}
	if ev == nil {
		return "", fmt.Errorf("empty ESL response for %q", cmd)
	}
	return ev.Body, nil
}

// Close terminates the ESL connection.
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
}
