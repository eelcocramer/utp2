package main

import (
	"fmt"
	"runtime"

	"github.com/h2so5/utp2"
)

func main() {
	numcpu := runtime.NumCPU()
	runtime.GOMAXPROCS(numcpu)
	for {
		addr, err := utp.ResolveAddr("utp", "")
		listener, err := utp.Listen("utp", addr)
		fmt.Println(listener.RawConn.LocalAddr(), err)

		b := make([]byte, 500)
		listener.RawConn.Close()
		n, addrx, err := listener.RawConn.ReadFrom(b)
		fmt.Println(n, addrx, err, "*", runtime.NumGoroutine())
		listener.AcceptUTP()
	}
}
