package utp

import (
	"errors"
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
	conn  net.PacketConn
	raddr net.Addr

	rdeadline     time.Time
	wdeadline     time.Time
	deadlineMutex sync.RWMutex
}

func newDialerConn(conn net.PacketConn, raddr *Addr) *dialerConn {
	c := &dialerConn{
		conn:  conn,
		raddr: raddr,
	}
	return c
}

func (c *dialerConn) Read(b []byte) (int, error) {
	return 0, nil
}

func (c *dialerConn) Write(b []byte) (int, error) {
	return 0, nil
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
