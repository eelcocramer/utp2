package utp

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"net"
	"sync"
	"time"
)

// A Dialer contains options for connecting to an address.
//
// The zero value for each field is equivalent to dialing without
// that option. Dialing with the zero value of Dialer is therefore
// equivalent to just calling the Dial function.
type Dialer struct {
	// Timeout is the maximum amount of time a dial will wait for
	// a connect to complete. If Deadline is also set, it may fail
	// earlier.
	//
	// The default is no timeout.
	Timeout time.Duration

	// LocalAddr is the local address to use when dialing an
	// address. The address must be of a compatible type for the
	// network being dialed.
	// If nil, a local address is automatically chosen.
	LocalAddr net.Addr
}

// Dial connects to the address on the named network.
//
// See func Dial for a description of the network and address parameters.
func (d *Dialer) Dial(n, addr string) (net.Conn, error) {
	raddr, err := ResolveAddr(n, addr)
	if err != nil {
		return nil, err
	}

	var laddr *Addr
	if d.LocalAddr != nil {
		var ok bool
		laddr, ok = d.LocalAddr.(*Addr)
		if !ok {
			return nil, errors.New("Dialer.LocalAddr is not a Addr")
		}
	}

	return DialUTPTimeout(n, laddr, raddr, d.Timeout)
}

// DialUTPTimeout acts like Dial but takes a timeout.
// The timeout includes name resolution, if required.
func DialUTPTimeout(n string, laddr, raddr *Addr, timeout time.Duration) (net.Conn, error) {

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

	return newDialerConn(conn, raddr), nil
}

type dialerConn struct {
	conn               net.PacketConn
	raddr              *Addr
	rid, sid, seq, ack uint16
	diff               uint32

	recvBuf  *ringBuffer
	recvRest []byte
	sendBuf  *ringBuffer

	rdeadline     time.Time
	wdeadline     time.Time
	deadlineMutex sync.RWMutex

	outOfBandBuf *ringQueue
}

func newDialerConn(conn net.PacketConn, raddr *Addr) *dialerConn {
	id := uint16(rand.Intn(math.MaxUint16))
	c := &dialerConn{
		conn:         conn,
		raddr:        raddr,
		rid:          id,
		sid:          id + 1,
		seq:          1,
		ack:          0,
		recvBuf:      NewRingBuffer(windowSize, 1),
		sendBuf:      NewRingBuffer(windowSize, 1),
		outOfBandBuf: NewRingQueue(outOfBandBufferSize),
	}
	c.sendSYN()
	go c.listen()
	return c
}

func (c *dialerConn) Read(b []byte) (int, error) {
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

func (c *dialerConn) Write(b []byte) (int, error) {
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

func (c *dialerConn) Close() error         { return nil }
func (c *dialerConn) LocalAddr() net.Addr  { return nil }
func (c *dialerConn) RemoteAddr() net.Addr { return nil }

func (c *dialerConn) SetDeadline(t time.Time) error {
	err := c.SetReadDeadline(t)
	if err != nil {
		return err
	}
	return c.SetWriteDeadline(t)
}

func (c *dialerConn) SetReadDeadline(t time.Time) error {
	c.deadlineMutex.Lock()
	defer c.deadlineMutex.Unlock()
	c.rdeadline = t
	return nil
}

func (c *dialerConn) SetWriteDeadline(t time.Time) error {
	c.deadlineMutex.Lock()
	defer c.deadlineMutex.Unlock()
	c.wdeadline = t
	return nil
}

func (c *dialerConn) listen() {
	for {
		var buf [maxUdpPayload]byte
		n, addr, err := c.conn.ReadFrom(buf[:])
		if err != nil {
			c.outOfBandBuf.Close()
			return
		}

		p, err := decodePacket(buf[:n])
		if err != nil {
			c.outOfBandBuf.Push(&udpPacket{b: buf[:n], addr: addr})
		} else {
			c.processPacket(p)
		}
	}
}

func (c *dialerConn) processPacket(p *packet) {
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
		if p.header.ack == 1 {
			c.recvBuf.SetSeq(p.header.seq)
		}
		c.sendBuf.EraseAll(p.header.ack)
	case stFin:
	}
	fmt.Println("#", p)
}

func (c *dialerConn) send(p *packet) error {
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

func (c *dialerConn) sendSYN() {
	syn := c.makePacket(stSyn, nil, c.raddr)
	c.send(syn)
}

func (c *dialerConn) sendACK() {
	ack := c.makePacket(stState, nil, c.raddr)
	c.send(ack)
}

func (c *dialerConn) sendDATA(b []byte) (int, error) {
	data := c.makePacket(stData, b, c.raddr)
	c.send(data)
	return len(b), nil
}

func (c *dialerConn) makePacket(typ int, payload []byte, dst *Addr) *packet {
	wnd := windowSize * mtu
	if c.recvBuf != nil {
		wnd = c.recvBuf.Window() * mtu
	}
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

/*
import (
	"errors"
	"math"
	"math/rand"
	"net"
	"time"
)

// DialUTP connects to the remote address raddr on the network net,
// which must be "utp", "utp4", or "utp6".  If laddr is not nil, it is
// used as the local address for the connection.
func DialUTP(n string, laddr, raddr *Addr) (*Conn, error) {
	return DialUTPTimeout(n, laddr, raddr, 0)
}

// DialUTPTimeout acts like Dial but takes a timeout.
// The timeout includes name resolution, if required.
func DialUTPTimeout(n string, laddr, raddr *Addr, timeout time.Duration) (*Conn, error) {
	conn, err := getSharedlistenerBaseConn(n, laddr)
	if err != nil {
		return nil, err
	}

	id := uint16(rand.Intn(math.MaxUint16))
	c := newConn()
	c.conn = conn
	c.raddr = raddr.Addr
	c.rid = id
	c.sid = id + 1
	c.seq = 1
	c.state = stateSynSent
	c.sendbuf = newPacketBuffer(windowSize*2, 1)
	c.conn.Register(int32(c.rid), c.recv)
	go c.loop()
	c.synch <- 0

	t := time.NewTimer(timeout)
	defer t.Stop()
	if timeout == 0 {
		t.Stop()
	}

	select {
	case <-c.connch:
	case <-t.C:
		c.Close()
		return nil, &net.OpError{
			Op:   "dial",
			Net:  c.LocalAddr().Network(),
			Addr: c.LocalAddr(),
			Err:  errTimeout,
		}
	}
	return c, nil
}
*/
