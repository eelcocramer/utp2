package main

import (
	"fmt"

	"github.com/h2so5/utp2"
)

func main() {
	addr, err := utp.ResolveAddr("utp", "")
	listener, err := utp.Listen("utp", addr)
	fmt.Println(listener.RawConn.LocalAddr(), err)

	b := make([]byte, 500)
	n, addrx, err := listener.RawConn.ReadFrom(b)
	fmt.Println(n, addrx, err)
	//conn, err := listener.AcceptUTP()
	//fmt.Println(conn)
}
