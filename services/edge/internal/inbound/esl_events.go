package inbound

import (
	"github.com/fiorix/go-eventsocket/eventsocket"
)

type eslEventConn struct {
	c *eventsocket.Connection
}

func (e *eslEventConn) Send(cmd string) error {
	_, err := e.c.Send(cmd)
	return err
}

func (e *eslEventConn) ReadEvent() (headerGetter, error) {
	ev, err := e.c.ReadEvent()
	if err != nil {
		return nil, err
	}
	return ev, nil
}

func (e *eslEventConn) Close() {
	e.c.Close()
}

func defaultDialEventConn(addr, password string) (EventConn, error) {
	c, err := eventsocket.Dial(addr, password)
	if err != nil {
		return nil, err
	}
	return &eslEventConn{c: c}, nil
}
