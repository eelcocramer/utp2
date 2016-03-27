package utp

import (
	"errors"
	"net"
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
func (d *Dialer) Dial(n, addr string) (*Conn, error) {
	raddr, err := ResolveAddr(n, addr)
	if err != nil {
		return nil, err
	}

	var laddr *Addr
	if d.LocalAddr != nil {
		var ok bool
		laddr, ok = d.LocalAddr.(*Addr)
		if !ok {
			return nil, errors.New("Dialer.LocalAddr is not an Addr")
		}
	}

	return DialUTPTimeout(n, laddr, raddr, d.Timeout)
}

// DialUTPTimeout acts like Dial but takes a timeout.
// The timeout includes name resolution, if required.
func DialUTPTimeout(n string, laddr, raddr *Addr, timeout time.Duration) (*Conn, error) {

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
