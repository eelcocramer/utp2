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

// Conn is an implementation of the Conn interface for UTP network
// connections.
type Conn struct {
	// RawConn represents an out-of-band connection.
	// This allows a single socket to handle multiple protocols.
	RawConn net.PacketConn

	conn net.PacketConn

	raddr              *Addr
	rid, sid, seq, ack uint16
	diff               uint32

	recvBuf  *ringBuffer
	recvRest []byte
	sendBuf  *ringBuffer

	sendChan  chan *frame
	recvChan  chan *packet
	closeChan chan int

	state int
	eos   int

	rdeadline     time.Time
	wdeadline     time.Time
	deadlineMutex sync.RWMutex

	outOfBandBuf *ringQueue
}

type udpPacket struct {
	addr net.Addr
	b    []byte
}

type frame struct {
	typ     int
	payload []byte
	dst     *Addr
}

func newDialerConn(conn net.PacketConn, raddr *Addr) *Conn {
	id := uint16(rand.Intn(math.MaxUint16))
	c := &Conn{
		RawConn:      conn,
		conn:         conn,
		raddr:        raddr,
		rid:          id,
		sid:          id + 1,
		seq:          1,
		ack:          0,
		recvBuf:      newRingBuffer(windowSize, 1),
		sendBuf:      newRingBuffer(windowSize, 1),
		sendChan:     make(chan *frame, 1),
		recvChan:     make(chan *packet, 1),
		closeChan:    make(chan int),
		state:        stateSynSent,
		eos:          -1,
		outOfBandBuf: newRingQueue(outOfBandBufferSize),
	}
	go c.loop()
	go c.listen()
	c.sendSYN()
	return c
}

func newListenerConn(bcon *listenerBaseConn, p *packet) *Conn {
	seq := rand.Intn(math.MaxUint16)
	c := &Conn{
		RawConn:   bcon,
		conn:      bcon.conn,
		raddr:     p.addr,
		rid:       p.header.id + 1,
		sid:       p.header.id,
		seq:       uint16(seq),
		ack:       p.header.seq,
		recvBuf:   newRingBuffer(windowSize, p.header.seq+1),
		sendBuf:   newRingBuffer(windowSize, uint16(seq)),
		sendChan:  make(chan *frame, 1),
		recvChan:  make(chan *packet, 1),
		closeChan: make(chan int),
		state:     stateSynRecv,
		eos:       -1,
	}
	go c.loop()
	c.sendACK()
	return c
}

func (c *Conn) ok() bool { return c != nil && c.conn != nil }

func (c *Conn) loop() {
	for {
		select {
		case p := <-c.recvChan:
			if p == nil {
				return
			}
			c.processPacket(p)
		case f := <-c.sendChan:
			if f == nil {
				return
			}
			c.send(c.makePacket(f))
		}
	}
}

func (c *Conn) listen() {
	for {
		var buf [maxUDPPayload]byte
		n, addr, err := c.conn.ReadFrom(buf[:])
		if err != nil {
			c.outOfBandBuf.Close()
			close(c.recvChan)
			return
		}

		p, err := decodePacket(buf[:n])
		if err != nil {
			c.outOfBandBuf.Push(&udpPacket{b: buf[:n], addr: addr})
		} else {
			c.recvChan <- p
		}
	}
}

func (c *Conn) send(p *packet) error {
	if p.header.typ == stFin {
		c.state = stateFinSent
		c.closeSendBuf()
	}
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

func (c *Conn) processPacket(p *packet) {
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
		if c.state == stateSynSent {
			c.recvBuf.SetSeq(p.header.seq)
			c.state = stateConnected
		}
		c.sendBuf.EraseAll(p.header.ack)
	case stFin:
		if c.eos < 0 {
			c.eos = int(p.header.seq)
		}
	case stReset:
		c.closeRecvBuf()
		c.closeSendBuf()
	}

	if c.eos >= 0 && c.eos == (int(c.ack)+1)%65536 {
		c.closeRecvBuf()
	}

	fmt.Println("#", p)
}

func (c *Conn) sendSYN() {
	c.sendChan <- &frame{typ: stSyn, payload: nil, dst: c.raddr}
}

func (c *Conn) sendACK() {
	c.sendChan <- &frame{typ: stState, payload: nil, dst: c.raddr}
}

func (c *Conn) sendFIN() {
	c.sendChan <- &frame{typ: stFin, payload: nil, dst: c.raddr}
}

func (c *Conn) sendRESET() {
	c.sendChan <- &frame{typ: stReset, payload: nil, dst: c.raddr}
}

func (c *Conn) sendDATA(b []byte) (int, error) {
	c.sendChan <- &frame{typ: stData, payload: b, dst: c.raddr}
	return len(b), nil
}

func (c *Conn) closeRecvBuf() {
	c.recvBuf.Close()
	if c.sendBuf.IsClosed() && c.RawConn == c.conn {
		c.conn.Close()
	}
}

func (c *Conn) closeSendBuf() {
	c.sendBuf.Close()
	if c.recvBuf.IsClosed() && c.RawConn == c.conn {
		c.conn.Close()
	}
}

func (c *Conn) makePacket(f *frame) *packet {
	wnd := c.recvBuf.Window() * mtu
	id := c.sid
	if f.typ == stSyn {
		id = c.rid
	}
	p := &packet{}
	p.header.typ = f.typ
	p.header.ver = version
	p.header.id = id
	p.header.t = currentMicrosecond()
	p.header.diff = c.diff
	p.header.wnd = uint32(wnd)
	p.header.seq = c.seq
	p.header.ack = c.ack
	p.addr = f.dst
	if f.typ != stState && f.typ != stFin {
		c.seq++
	}
	p.payload = f.payload
	return p
}

// Read implements the Conn Read method.
func (c *Conn) Read(b []byte) (int, error) {
	if !c.ok() {
		return 0, syscall.EINVAL
	}
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

// Write implements the Conn Write method.
func (c *Conn) Write(b []byte) (int, error) {
	if !c.ok() {
		return 0, syscall.EINVAL
	}
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

// Close closes the connection.
func (c *Conn) Close() error {
	if !c.ok() {
		return syscall.EINVAL
	}
	select {
	case <-c.closeChan:
	default:
		close(c.closeChan)
		c.sendFIN()
		c.closeSendBuf()
	}
	return nil
}

// LocalAddr returns the local network address.
func (c *Conn) LocalAddr() net.Addr {
	if !c.ok() {
		return nil
	}
	return c.conn.LocalAddr()
}

// RemoteAddr returns the remote network address.
func (c *Conn) RemoteAddr() net.Addr {
	if !c.ok() {
		return nil
	}
	return c.raddr
}

// SetDeadline implements the Conn SetDeadline method.
func (c *Conn) SetDeadline(t time.Time) error {
	if !c.ok() {
		return syscall.EINVAL
	}
	err := c.SetReadDeadline(t)
	if err != nil {
		return err
	}
	return c.SetWriteDeadline(t)
}

// SetReadDeadline implements the Conn SetReadDeadline method.
func (c *Conn) SetReadDeadline(t time.Time) error {
	if !c.ok() {
		return syscall.EINVAL
	}
	c.deadlineMutex.Lock()
	defer c.deadlineMutex.Unlock()
	c.rdeadline = t
	return nil
}

// SetWriteDeadline implements the Conn SetWriteDeadline method.
func (c *Conn) SetWriteDeadline(t time.Time) error {
	if !c.ok() {
		return syscall.EINVAL
	}
	c.deadlineMutex.Lock()
	defer c.deadlineMutex.Unlock()
	c.wdeadline = t
	return nil
}

func currentMicrosecond() uint32 {
	return uint32(time.Now().Nanosecond() / 1000)
}

func decodePacket(b []byte) (*packet, error) {
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
