package utp

import (
	"net"
	"sync"
	"syscall"
	"time"
)

// Listener is a UTP network listener.  Clients should typically
// use variables of type Listener instead of assuming UTP.
type Listener struct {
	// RawConn represents an out-of-band connection.
	// This allows a single socket to handle multiple protocols.
	RawConn net.PacketConn

	conn *listenerBaseConn
}

// Listen announces on the UTP address laddr and returns a UTP
// listener.  Net must be "utp", "utp4", or "utp6".  If laddr has a
// port of 0, ListenUTP will choose an available port.  The caller can
// use the Addr method of Listener to retrieve the chosen address.
func Listen(n string, laddr *Addr) (*Listener, error) {
	udpnet, err := utp2udp(n)
	if err != nil {
		return nil, err
	}
	s := ":0"
	if laddr != nil {
		s = laddr.String()
	}
	conn, err := net.ListenPacket(udpnet, s)
	if err != nil {
		return nil, err
	}
	b := newListenerBaseConn(conn)
	l := &Listener{
		RawConn: b,
		conn:    b,
	}
	return l, nil
}

// AcceptUTP accepts the next incoming call and returns the new
// connection.
func (l *Listener) AcceptUTP() (net.Conn, error) {
	return l.conn.accept()
}

type listenerBaseConn struct {
	conn    net.PacketConn
	sockets map[uint16]*Conn

	recvChan  chan *udpPacket
	closeChan chan int

	outOfBandBuf      *ringQueue
	waitingSocketsBuf *ringQueue
	m                 sync.RWMutex
}

func newListenerBaseConn(conn net.PacketConn) *listenerBaseConn {
	c := &listenerBaseConn{
		conn:              conn,
		sockets:           make(map[uint16]*Conn),
		recvChan:          make(chan *udpPacket),
		closeChan:         make(chan int),
		outOfBandBuf:      newRingQueue(outOfBandBufferSize),
		waitingSocketsBuf: newRingQueue(waitingSocketsBufferSize),
	}
	go c.listen()
	return c
}

func (c *listenerBaseConn) ok() bool { return c != nil && c.conn != nil }

func (c *listenerBaseConn) ReadFrom(b []byte) (n int, addr net.Addr, err error) {
	if !c.ok() {
		return 0, nil, syscall.EINVAL
	}
	i, err := c.outOfBandBuf.Pop()
	if err != nil {
		return 0, nil, err
	}
	p := i.(*udpPacket)
	return copy(b, p.b), p.addr, nil
}

func (c *listenerBaseConn) WriteTo(b []byte, addr net.Addr) (n int, err error) {
	if !c.ok() {
		return 0, syscall.EINVAL
	}
	return c.conn.WriteTo(b, addr)
}

func (c *listenerBaseConn) Close() error {
	if !c.ok() {
		return syscall.EINVAL
	}
	select {
	case <-c.closeChan:
		return errClosing
	default:
		close(c.closeChan)
		c.conn.Close()
	}
	return nil
}

func (c *listenerBaseConn) LocalAddr() net.Addr {
	return &Addr{Addr: c.conn.LocalAddr()}
}

func (c *listenerBaseConn) SetDeadline(t time.Time) error {
	if !c.ok() {
		return syscall.EINVAL
	}
	return nil
}

func (c *listenerBaseConn) SetReadDeadline(t time.Time) error {
	if !c.ok() {
		return syscall.EINVAL
	}
	return nil
}

func (c *listenerBaseConn) SetWriteDeadline(t time.Time) error {
	if !c.ok() {
		return syscall.EINVAL
	}
	return nil
}

func (c *listenerBaseConn) listen() {
	for {
		var buf [maxUDPPayload]byte
		n, addr, err := c.conn.ReadFrom(buf[:])
		if err != nil {
			c.outOfBandBuf.Close()
			c.waitingSocketsBuf.Close()
			return
		}

		p, err := decodePacket(buf[:n])
		if err != nil {
			c.outOfBandBuf.Push(&udpPacket{b: buf[:n], addr: addr})
		} else {
			p.addr = &Addr{Addr: addr}
			if p.header.typ == stSyn {
				if c.waitingSocketsBuf.Get(p.header.id+1) == nil {
					c.waitingSocketsBuf.Push(newListenerConn(c, p))
				}
			} else if i := c.waitingSocketsBuf.Get(p.header.id); i != nil {
				i.(*Conn).recvChan <- p
			} else {
				c.m.RLock()
				s := c.sockets[p.header.id]
				c.m.RUnlock()
				if s != nil {
					s.recvChan <- p
				}
			}
		}
	}
}

func (c *listenerBaseConn) accept() (*Conn, error) {
	i, err := c.waitingSocketsBuf.Pop()
	if err != nil {
		return nil, err
	}
	c.m.Lock()
	defer c.m.Unlock()
	conn := i.(*Conn)
	c.sockets[conn.rid] = conn
	return conn, nil
}

func (c *listenerBaseConn) send(p *packet) error {
	b, err := p.MarshalBinary()
	if err != nil {
		return err
	}
	_, err = c.conn.WriteTo(b, p.addr.Addr)
	if err != nil {
		return err
	}
	return nil
}
