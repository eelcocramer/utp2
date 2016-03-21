package utp

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
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
	go b.listen()
	return l, nil
}

// AcceptUTP accepts the next incoming call and returns the new
// connection.
func (l *Listener) AcceptUTP() (net.Conn, error) {
	return l.conn.accept()
}

type listenerBaseConn struct {
	conn    net.PacketConn
	sockets map[uint16]*listenerConn

	recvChan  chan *udpPacket
	closeChan chan int

	outOfBandBuf      *ringQueue
	waitingSocketsBuf *ringQueue
	m                 sync.RWMutex
}

type udpPacket struct {
	addr net.Addr
	b    []byte
}

func newListenerBaseConn(conn net.PacketConn) *listenerBaseConn {
	c := &listenerBaseConn{
		conn:              conn,
		sockets:           make(map[uint16]*listenerConn),
		recvChan:          make(chan *udpPacket),
		closeChan:         make(chan int),
		outOfBandBuf:      NewRingQueue(outOfBandBufferSize),
		waitingSocketsBuf: NewRingQueue(waitingSocketsBufferSize),
	}
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
		var buf [maxUdpPayload]byte
		n, addr, err := c.conn.ReadFrom(buf[:])
		if err != nil {
			c.outOfBandBuf.Close()
			c.waitingSocketsBuf.Close()
			return
		}

		p, err := c.decodePacket(buf[:n])
		if err != nil {
			c.outOfBandBuf.Push(&udpPacket{b: buf[:n], addr: addr})
		} else {
			p.addr = addr
			if p.header.typ == stSyn {
				if c.waitingSocketsBuf.Get(p.header.id+1) == nil {
					c.waitingSocketsBuf.Push(newListenerConn(c, p))
				}
			} else if i := c.waitingSocketsBuf.Get(p.header.id); i != nil {
				i.(*listenerConn).processPacket(p)
			} else {
				c.m.RLock()
				s := c.sockets[p.header.id]
				c.m.RUnlock()
				if s != nil {
					s.processPacket(p)
				}
			}
		}
	}
}

func (c *listenerBaseConn) accept() (*listenerConn, error) {
	i, err := c.waitingSocketsBuf.Pop()
	if err != nil {
		return nil, err
	}
	c.m.Lock()
	defer c.m.Unlock()
	conn := i.(*listenerConn)
	c.sockets[conn.rid] = conn
	return conn, nil
}

func (c *listenerBaseConn) send(p *packet) error {
	b, err := p.MarshalBinary()
	if err != nil {
		return err
	}
	_, err = c.conn.WriteTo(b, p.addr)
	if err != nil {
		return err
	}
	return nil
}

func (c *listenerBaseConn) decodePacket(b []byte) (*packet, error) {
	var p packet
	err := p.UnmarshalBinary(b)
	if err != nil {
		return nil, err
	}
	if p.header.ver != version {
		return nil, errors.New("unsupported utp version")
	}
	return &p, nil
}

type listenerConn struct {
	bcon               *listenerBaseConn
	raddr              net.Addr
	rid, sid, seq, ack uint16
	diff               uint32

	recvBuf  *ringBuffer
	recvRest []byte
	sendBuf  *ringBuffer
}

func newListenerConn(bcon *listenerBaseConn, p *packet) *listenerConn {
	seq := rand.Intn(math.MaxUint16)
	c := &listenerConn{
		bcon:    bcon,
		raddr:   p.addr,
		rid:     p.header.id + 1,
		sid:     p.header.id,
		seq:     uint16(seq),
		ack:     p.header.seq,
		recvBuf: NewRingBuffer(128, p.header.seq+1),
		sendBuf: NewRingBuffer(128, uint16(seq)),
	}

	c.sendACK()
	return c
}

func (c *listenerConn) processPacket(p *packet) {
	if p.header.t == 0 {
		c.diff = 0
	} else {
		t := currentMicrosecond()
		if t > p.header.t {
			c.diff = t - p.header.t
		}
	}

	switch p.header.typ {
	case stData:
		c.recvBuf.Put(p.payload, p.header.seq)
		c.ack = c.recvBuf.Ack()
		c.sendACK()
	case stState:
		c.sendBuf.Erase(p.header.ack)
	case stFin:
	}
	fmt.Println("#", p)
}

func (c *listenerConn) sendACK() {
	ack := c.makePacket(stState, nil, c.raddr)
	c.bcon.send(ack)
}

func (c *listenerConn) sendDATA(b []byte) (int, error) {
	data := c.makePacket(stData, b, c.raddr)
	c.bcon.send(data)
	return len(b), nil
}

func (c *listenerConn) Read(b []byte) (int, error) {
	if len(c.recvRest) > 0 {
		l := copy(b, c.recvRest)
		c.recvRest = c.recvRest[l:]
		return l, nil
	}
	p, err := c.recvBuf.Pop()
	if err != nil {
		return 0, err
	}
	l := copy(b, p)
	c.recvRest = p[l:]
	return l, nil
}

func (c *listenerConn) Write(b []byte) (int, error) {
	payload := b
	if len(payload) > mss {
		payload = payload[:mss]
	}
	_, err := c.sendBuf.Push(payload)
	if err != nil {
		return 0, err
	}
	l, err := c.sendDATA(payload)
	if err != nil {
		return 0, err
	}
	return l, nil
}

func (c *listenerConn) Close() error                       { return nil }
func (c *listenerConn) LocalAddr() net.Addr                { return c.bcon.conn.LocalAddr() }
func (c *listenerConn) RemoteAddr() net.Addr               { return c.raddr }
func (c *listenerConn) SetDeadline(t time.Time) error      { return nil }
func (c *listenerConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *listenerConn) SetWriteDeadline(t time.Time) error { return nil }
func (c *listenerConn) index() uint16 {
	return c.rid
}

func (c *listenerConn) makePacket(typ int, payload []byte, dst net.Addr) *packet {
	wnd := windowSize * mtu
	id := c.sid
	if typ == stSyn {
		id = c.rid
	}
	p := &packet{}
	p.header.typ = typ
	p.header.ver = version
	p.header.id = id
	p.header.t = currentMicrosecond()
	p.header.diff = c.diff
	p.header.wnd = uint32(wnd)
	p.header.seq = c.seq
	p.header.ack = c.ack
	p.addr = dst
	if typ != stState && typ != stFin {
		c.seq++
	}
	p.payload = payload
	return p
}

func currentMicrosecond() uint32 {
	return uint32(time.Now().Nanosecond() / 1000)
}

/*
import (
	"math"
	"math/rand"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Listener is a UTP network listener.  Clients should typically
// use variables of type Listener instead of assuming UTP.
type Listener struct {
	// RawConn represents an out-of-band connection.
	// This allows a single socket to handle multiple protocols.
	RawConn net.PacketConn

	conn          *listenerBaseConn
	deadline      time.Time
	deadlineMutex sync.RWMutex
	closed        int32
}

func (l *Listener) ok() bool { return l != nil && l.conn != nil }

// Listen announces on the UTP address laddr and returns a UTP
// listener.  Net must be "utp", "utp4", or "utp6".  If laddr has a
// port of 0, ListenUTP will choose an available port.  The caller can
// use the Addr method of Listener to retrieve the chosen address.
func Listen(n string, laddr *Addr) (*Listener, error) {
	conn, err := newListenerBaseConn(n, laddr)
	if err != nil {
		return nil, err
	}
	l := &Listener{
		RawConn: conn,
		conn:    conn,
	}
	conn.Register(-1, nil)
	return l, nil
}

// Accept implements the Accept method in the Listener interface; it
// waits for the next call and returns a generic Conn.
func (l *Listener) Accept() (net.Conn, error) {
	return l.AcceptUTP()
}

// AcceptUTP accepts the next incoming call and returns the new
// connection.
func (l *Listener) AcceptUTP() (*Conn, error) {
	if !l.ok() {
		return nil, syscall.EINVAL
	}
	if !l.isOpen() {
		return nil, &net.OpError{
			Op:   "accept",
			Net:  l.conn.LocalAddr().Network(),
			Addr: l.conn.LocalAddr(),
			Err:  errClosing,
		}
	}
	l.deadlineMutex.RLock()
	d := timeToDeadline(l.deadline)
	l.deadlineMutex.RUnlock()
	p, err := l.conn.RecvSyn(d)
	if err != nil {
		return nil, &net.OpError{
			Op:   "accept",
			Net:  l.conn.LocalAddr().Network(),
			Addr: l.conn.LocalAddr(),
			Err:  errClosing,
		}
	}

	seq := rand.Intn(math.MaxUint16)
	rid := p.header.id + 1

	c := newConn()
	c.state = stateConnected
	c.conn = l.conn
	c.raddr = p.addr
	c.rid = p.header.id + 1
	c.sid = p.header.id
	c.seq = uint16(seq)
	c.ack = p.header.seq
	c.recvbuf = newPacketBuffer(windowSize, int(p.header.seq))
	c.sendbuf = newPacketBuffer(windowSize*2, seq)
	l.conn.Register(int32(rid), c.recv)
	go c.loop()
	c.recv <- p

	ulog.Printf(2, "listenerBaseConn(%v): accept #%d from %v", c.LocalAddr(), c.rid, c.raddr)
	return c, nil
}

// Addr returns the listener's network address, a *Addr.
func (l *Listener) Addr() net.Addr {
	if !l.ok() {
		return nil
	}
	return l.conn.LocalAddr()
}

// Close stops listening on the UTP address.
// Already Accepted connections are not closed.
func (l *Listener) Close() error {
	if !l.ok() {
		return syscall.EINVAL
	}
	if !l.close() {
		return &net.OpError{
			Op:   "close",
			Net:  l.conn.LocalAddr().Network(),
			Addr: l.conn.LocalAddr(),
			Err:  errClosing,
		}
	}
	return nil
}

// SetDeadline sets the deadline associated with the listener.
// A zero time value disables the deadline.
func (l *Listener) SetDeadline(t time.Time) error {
	if !l.ok() {
		return syscall.EINVAL
	}
	l.deadlineMutex.Lock()
	defer l.deadlineMutex.Unlock()
	l.deadline = t
	return nil
}

func (l *Listener) close() bool {
	if atomic.CompareAndSwapInt32(&l.closed, 0, 1) {
		l.conn.Unregister(-1)
		return true
	}
	return false
}

func (l *Listener) isOpen() bool {
	return atomic.LoadInt32(&l.closed) == 0
}
*/
